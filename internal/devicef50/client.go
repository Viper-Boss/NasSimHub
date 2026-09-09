// Package devicef50 implements a client for ZTE F50-class 5G MiFi devices
// (F50 / F50 Pro / V50 / U30 Air / 飞猫 U20 with ZTE firmware). It talks to
// the stock ZTE web backend on port 80 (goform endpoints) and, optionally,
// to UFI advanced backends exposing the same goform surface on /api.
//
// Protocol notes were derived from koldllc/f50-monitor (MIT). Login uses
// GET cmd=LD, then POST goformId=LOGIN with
// password = SHA256(SHA256(plaintext).upper + LD).upper. A session cookie
// from the login response is required for every subsequent call together
// with the Referer header. SMS bodies in sms_data_total are base64.
package devicef50

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is a ZTE F50 web-backend client. It is safe for concurrent use
// after the first Login; the gateway serialises calls per device anyway.
type Client struct {
	BaseURL    string // e.g. http://192.168.0.1 (no trailing slash)
	Username   string // default "admin"
	Password   string // plaintext, or an uppercase SHA-256 hex to use directly
	HTTPClient *http.Client

	cookie       string
	versions     [2]string // wa_inner_version, cr_version (for the AD token)
	loggedIn     bool
	cachedAD     string
	adTokenFresh bool
}

// New creates a client with sane HTTP timeouts.
func New(baseURL, username, password string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Username:   username,
		Password:   password,
		HTTPClient: &http.Client{Timeout: 8 * time.Second},
	}
}

// Status is the merged device status snapshot.
type Status struct {
	NetworkType     string
	Provider        string
	Signalbar       string
	RSRP            string
	RSRQ            string
	SNR             string
	Bands           string
	LTEBand         string
	NRBand          string
	RealtimeRxThrpt string
	RealtimeTxThrpt string
	MonthlyRxBytes  string
	MonthlyTxBytes  string
	TotalRxBytes    string
	TotalTxBytes    string
	BillingDay      string
	ClearDate       string
	DataLimitSize   string
	DataLimitUnit   string
	Temperature     string
	CPUTemp         string
	ICTemp          string
	CPUUtility      string
	MemUtility      string
	WifiStaNum      string
	SMSUnread       string
	FirmwareWA      string
	FirmwareCR      string
}

// SMSMessage is one entry from sms_data_total. Content is decoded UTF-8.
type SMSMessage struct {
	ID      string
	Number  string
	Content string
	Date    string
	Tag     string // "0" unread, "1" read (ZTE convention varies per firmware)
}

var errUnauthorized = errors.New("F50 session expired")

func (c *Client) referer() string { return c.BaseURL + "/index.html" }

func sha256Upper(text string) string {
	digest := sha256.Sum256([]byte(text))
	return strings.ToUpper(hex.EncodeToString(digest[:]))
}

// loginPassword implements SHA256(SHA256(plaintext).upper + LD).upper. An
// input that already looks like a 64-char SHA-256 hex is used as the first
// round directly (same behaviour as f50-monitor).
func (c *Client) loginPassword(ld string) string {
	trimmed := strings.TrimSpace(c.Password)
	first := trimmed
	if len(trimmed) != 64 || strings.ContainsAny(trimmed, "ghijklmnopqrstuvwxyz") {
		first = sha256Upper(trimmed)
	}
	return sha256Upper(first + ld)
}

func (c *Client) fetchLD() (string, error) {
	body, err := c.getRaw("/goform/goform_get_cmd_process?isTest=false&cmd=LD&_=" + timestamp())
	if err != nil {
		return "", err
	}
	ld := strings.TrimSpace(body["LD"])
	if ld == "" {
		return "", errors.New("F50: empty LD token")
	}
	return ld, nil
}

func (c *Client) login() error {
	ld, err := c.fetchLD()
	if err != nil {
		return err
	}
	form := url.Values{}
	form.Set("goformId", "LOGIN")
	form.Set("isTest", "false")
	form.Set("user", c.Username)
	form.Set("password", c.loginPassword(ld))
	request, err := http.NewRequest(http.MethodPost, c.BaseURL+"/goform/goform_set_cmd_process", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Referer", c.referer())
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	for _, cookie := range response.Cookies() {
		if cookie.Value != "" {
			c.cookie = cookie.Name + "=" + cookie.Value
			c.loggedIn = true
			return nil
		}
	}
	var payload map[string]any
	_ = json.NewDecoder(response.Body).Decode(&payload)
	switch fmt.Sprint(payload["result"]) {
	case "0", "3":
		c.loggedIn = true
		return nil
	}
	return errors.New("F50: login rejected (wrong password or firmware lock)")
}

// ensureSession performs the LD+LOGIN handshake when needed.
func (c *Client) ensureSession() error {
	if c.cookie == "" || !c.loggedIn {
		return c.login()
	}
	return nil
}

func timestamp() string { return strconv.FormatInt(time.Now().UnixMilli(), 10) }

func (c *Client) goformGet(cmds string) (map[string]string, error) {
	if err := c.ensureSession(); err != nil {
		return nil, err
	}
	body, err := c.getWithCookie("/goform/goform_get_cmd_process?multi_data=1&isTest=false&cmd=" + url.QueryEscape(cmds) + "&_=" + timestamp())
	if errors.Is(err, errUnauthorized) {
		c.cookie = ""
		c.loggedIn = false
		if err := c.login(); err != nil {
			return nil, err
		}
		body, err = c.getWithCookie("/goform/goform_get_cmd_process?multi_data=1&isTest=false&cmd=" + url.QueryEscape(cmds) + "&_=" + timestamp())
	}
	if err != nil {
		return nil, err
	}
	return body, nil
}

// decodeStringMap tolerates firmware fields that arrive as JSON numbers or
// booleans (e.g. wifi_access_sta_num, monthly_rx_bytes) by coercing every
// scalar to its string form. UseNumber keeps large byte counters exact.
func decodeStringMap(data io.Reader) (map[string]string, error) {
	var payload map[string]any
	decoder := json.NewDecoder(data)
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	result := make(map[string]string, len(payload))
	for key, value := range payload {
		switch typed := value.(type) {
		case string:
			result[key] = typed
		case json.Number:
			result[key] = typed.String()
		case bool:
			result[key] = strconv.FormatBool(typed)
		case nil:
			result[key] = ""
		default:
			result[key] = fmt.Sprint(value)
		}
	}
	return result, nil
}

func (c *Client) getRaw(path string) (map[string]string, error) {
	request, err := http.NewRequest(http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Referer", c.referer())
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, decodeErr := decodeStringMap(response.Body)
	if decodeErr != nil {
		return nil, fmt.Errorf("F50: cannot decode response: %w", decodeErr)
	}
	return payload, nil
}

func (c *Client) getWithCookie(path string) (map[string]string, error) {
	request, err := http.NewRequest(http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Referer", c.referer())
	if c.cookie != "" {
		request.Header.Set("Cookie", c.cookie)
	}
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusFound || response.StatusCode == http.StatusMovedPermanently {
		return nil, errUnauthorized
	}
	payload, decodeErr := decodeStringMap(response.Body)
	if decodeErr != nil {
		return nil, fmt.Errorf("F50: cannot decode response: %w", decodeErr)
	}
	return payload, nil
}

func (c *Client) goformPost(form url.Values) (map[string]any, error) {
	if err := c.ensureSession(); err != nil {
		return nil, err
	}
	result, err := c.postWithCookie("/goform/goform_set_cmd_process", form)
	if errors.Is(err, errUnauthorized) {
		c.cookie = ""
		c.loggedIn = false
		if err := c.login(); err != nil {
			return nil, err
		}
		return c.postWithCookie("/goform/goform_set_cmd_process", form)
	}
	return result, err
}

func (c *Client) postWithCookie(path string, form url.Values) (map[string]any, error) {
	request, err := http.NewRequest(http.MethodPost, c.BaseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Referer", c.referer())
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	if c.cookie != "" {
		request.Header.Set("Cookie", c.cookie)
	}
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusFound || response.StatusCode == http.StatusMovedPermanently {
		return nil, errUnauthorized
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("F50: cannot decode response: %w", err)
	}
	return payload, nil
}

const statusCommands = "network_type,network_provider,signalbar,network_signalbar,network_information," +
	"Z5g_rsrp,Z5g_rsrq,Z5g_snr,5g_rsrp,5g_rsrq,5g_snr,lte_rsrp,lte_rsrq,lte_snr," +
	"wan_active_band,lte_band,lte_ca_pcell_band,nr5g_action_band,nr5g_action_nsa_band," +
	"realtime_rx_thrpt,realtime_tx_thrpt,monthly_rx_bytes,monthly_tx_bytes," +
	"data_volume_limit_size,data_volume_limit_unit,monthly_clear_day,clear_date," +
	"temperature,cpu_temp,ic_temp,cpu_utility,mem_utility,wifi_access_sta_num," +
	"sms_unread_num,sms_sim_unread_num,wa_inner_version,cr_version"

// Status queries the merged device snapshot.
func (c *Client) Status() (*Status, error) {
	payload, err := c.goformGet(statusCommands)
	if err != nil {
		return nil, err
	}
	status := &Status{
		NetworkType:     payload["network_type"],
		Provider:        payload["network_provider"],
		Signalbar:       payload["signalbar"],
		RSRP:            firstOf(payload, "lte_rsrp", "5g_rsrp", "Z5g_rsrp"),
		RSRQ:            firstOf(payload, "lte_rsrq", "5g_rsrq", "Z5g_rsrq"),
		SNR:             firstOf(payload, "lte_snr", "5g_snr", "Z5g_snr"),
		Bands:           firstOf(payload, "wan_active_band", "nr5g_action_band", "lte_band"),
		LTEBand:         payload["lte_band"],
		NRBand:          firstOf(payload, "nr5g_action_band", "nr5g_action_nsa_band"),
		RealtimeRxThrpt: payload["realtime_rx_thrpt"],
		RealtimeTxThrpt: payload["realtime_tx_thrpt"],
		MonthlyRxBytes:  payload["monthly_rx_bytes"],
		MonthlyTxBytes:  payload["monthly_tx_bytes"],
		TotalRxBytes:    payload["total_rx_bytes"],
		TotalTxBytes:    payload["total_tx_bytes"],
		BillingDay:      firstOf(payload, "monthly_clear_day", "clear_date", "data_volume_clear_day"),
		ClearDate:       payload["clear_date"],
		DataLimitSize:   payload["data_volume_limit_size"],
		DataLimitUnit:   payload["data_volume_limit_unit"],
		Temperature:     payload["temperature"],
		CPUTemp:         payload["cpu_temp"],
		ICTemp:          payload["ic_temp"],
		CPUUtility:      payload["cpu_utility"],
		MemUtility:      payload["mem_utility"],
		WifiStaNum:      payload["wifi_access_sta_num"],
		SMSUnread:       firstOf(payload, "sms_unread_num", "sms_sim_unread_num"),
		FirmwareWA:      payload["wa_inner_version"],
		FirmwareCR:      payload["cr_version"],
	}
	c.versions = [2]string{payload["wa_inner_version"], payload["cr_version"]}
	return status, nil
}

func firstOf(payload map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := payload[key]; value != "" && value != "null" && value != "undefined" {
			return value
		}
	}
	return ""
}

// SMSList fetches one page of messages (page starts at 0, 100 per page).
func (c *Client) SMSList(page int) ([]SMSMessage, error) {
	path := "/goform/goform_get_cmd_process?multi_data=1&isTest=false&cmd=sms_data_total" +
		"&page=" + strconv.Itoa(page) + "&data_per_page=100&mem_store=1&tags=100" +
		"&order_by=" + url.QueryEscape("order by id desc") + "&_=" + timestamp()
	raw, err := c.getWithCookieJSON(path)
	if errors.Is(err, errUnauthorized) {
		c.cookie = ""
		c.loggedIn = false
		if err := c.login(); err != nil {
			return nil, err
		}
		raw, err = c.getWithCookieJSON(path)
	}
	if err != nil {
		return nil, err
	}
	rows, _ := raw["messages"].([]any)
	messages := make([]SMSMessage, 0, len(rows))
	for _, rowAny := range rows {
		row, ok := rowAny.(map[string]any)
		if !ok {
			continue
		}
		id := fmt.Sprint(row["id"])
		if id == "" || id == "<nil>" {
			continue
		}
		number := fmt.Sprint(row["number"])
		encoded := fmt.Sprint(row["content"])
		content := encoded
		if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
			content = string(decoded)
		}
		messages = append(messages, SMSMessage{
			ID:      id,
			Number:  number,
			Content: content,
			Date:    fmt.Sprint(row["date"]),
			Tag:     fmt.Sprint(row["tag"]),
		})
	}
	return messages, nil
}

func (c *Client) getWithCookieJSON(path string) (map[string]any, error) {
	request, err := http.NewRequest(http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Referer", c.referer())
	if c.cookie != "" {
		request.Header.Set("Cookie", c.cookie)
	}
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusFound || response.StatusCode == http.StatusMovedPermanently {
		return nil, errUnauthorized
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("F50: cannot decode response: %w", err)
	}
	return payload, nil
}

// adToken computes the ZTE anti-replay token:
// SHA256(SHA256(wa_inner_version + cr_version).upper + RD).upper.
func (c *Client) adToken() (string, error) {
	if c.cachedAD != "" && c.adTokenFresh {
		return c.cachedAD, nil
	}
	payload, err := c.goformGet("Language,cr_version,wa_inner_version,RD")
	if err != nil {
		return "", err
	}
	rd := strings.TrimSpace(payload["RD"])
	if rd == "" {
		return "", errors.New("F50: empty RD token")
	}
	versionHash := sha256Upper(payload["wa_inner_version"] + payload["cr_version"])
	c.cachedAD = sha256Upper(versionHash + rd)
	c.adTokenFresh = true
	return c.cachedAD, nil
}

// MarkRead flags messages as read.
func (c *Client) MarkRead(ids []string) error {
	ad, err := c.adToken()
	if err != nil {
		return err
	}
	form := url.Values{}
	form.Set("goformId", "SET_MSG_READ")
	form.Set("msg_id", strings.Join(ids, ";"))
	form.Set("tag", "0")
	form.Set("AD", ad)
	_, err = c.goformPost(form)
	return err
}

// smsTime formats the ZTE local-time stamp: yy;MM;dd;HH;mm;ss;+TZ.
func smsTime(now time.Time) string {
	base := now.Format("06;01;02;15;04;05")
	_, offset := now.Zone()
	hours := offset / 3600
	sign := "+"
	if hours < 0 {
		sign = "-"
		hours = -hours
	}
	return fmt.Sprintf("%s;%s%02d", base, sign, hours)
}

// gsmEncode hex-encodes text as UTF-16BE (the ZTE UNICODE encode_type).
func gsmEncode(text string) string {
	var hex strings.Builder
	for _, symbol := range text {
		code := uint32(symbol)
		if code > 0xFFFF {
			high := 0xD800 + ((code - 0x10000) >> 10)
			low := 0xDC00 + ((code - 0x10000) & 0x3FF)
			hex.WriteString(fmt.Sprintf("%04x%04x", high, low))
			continue
		}
		hex.WriteString(fmt.Sprintf("%04x", code))
	}
	return hex.String()
}

func sendSuccess(payload map[string]any) bool {
	switch fmt.Sprint(payload["result"]) {
	case "0", "3", "success":
		return true
	}
	return false
}

// SendSMS sends a message through goformId=SEND_SMS with the UNICODE
// encode_type (UTF-16BE hex body).
func (c *Client) SendSMS(number, content string) error {
	ad, err := c.adToken()
	if err != nil {
		return err
	}
	clean := strings.Map(func(symbol rune) rune {
		if symbol == ' ' || symbol == '-' || symbol == '(' || symbol == ')' {
			return -1
		}
		return symbol
	}, strings.TrimSpace(number))
	form := url.Values{}
	form.Set("isTest", "false")
	form.Set("goformId", "SEND_SMS")
	form.Set("notCallback", "true")
	form.Set("Number", clean)
	form.Set("sms_time", smsTime(time.Now()))
	form.Set("MessageBody", gsmEncode(content))
	form.Set("ID", "-1")
	form.Set("encode_type", "UNICODE")
	form.Set("AD", ad)
	result, err := c.goformPost(form)
	if err != nil {
		return err
	}
	if !sendSuccess(result) {
		return fmt.Errorf("F50: send rejected: %v", result["result"])
	}
	return nil
}
