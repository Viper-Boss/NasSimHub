package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/messageevents"
)

// HomeSIM SMS forwarding channels. Settings follow the NasSimHub integration
// rules: the channel list itself is server-side state (forwarders.json), and
// secret-looking values (ServerChan/PushPlus keys) are stored only as the
// name of the environment variable holding them, resolved at send time.

const forwarderRequestTimeout = 10 * time.Second

const maxForwarderChannels = 8

var forwarderTypes = map[string]bool{
	"webhook":    true,
	"dingtalk":   true,
	"feishu":     true,
	"wecom":      true,
	"serverchan": true,
	"pushplus":   true,
	"nkn":        true,
}

type homeSIMForwarderConfig struct {
	WebhookURL    string `json:"webhook_url,omitempty"`
	Secret        string `json:"secret,omitempty"`
	SecretSet     bool   `json:"secret_set,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty"`
	NKNTopic      string `json:"nkn_topic,omitempty"`
	NKNAddresses  string `json:"nkn_addresses,omitempty"`
}

type homeSIMForwarder struct {
	ID      string                 `json:"id"`
	Name    string                 `json:"name"`
	Type    string                 `json:"type"`
	Enabled bool                   `json:"enabled"`
	Events  []string               `json:"events"`
	Config  homeSIMForwarderConfig `json:"config"`
}

type homeSIMForwarderSettings struct {
	Revision   int64              `json:"revision"`
	Forwarders []homeSIMForwarder `json:"forwarders"`
}

func defaultHomeSIMForwarderSettings() homeSIMForwarderSettings {
	return homeSIMForwarderSettings{Revision: 1, Forwarders: []homeSIMForwarder{}}
}

func validHomeSIMForwarderSettings(settings homeSIMForwarderSettings) bool {
	if len(settings.Forwarders) > maxForwarderChannels {
		return false
	}
	seen := map[string]bool{}
	for _, forwarder := range settings.Forwarders {
		if forwarder.ID == "" || len(forwarder.ID) > 64 || seen[forwarder.ID] {
			return false
		}
		seen[forwarder.ID] = true
		if len(forwarder.Name) > 64 {
			return false
		}
		if !forwarderTypes[forwarder.Type] {
			return false
		}
		if len(forwarder.Events) == 0 {
			return false
		}
		for _, event := range forwarder.Events {
			if event != "sms" {
				return false
			}
		}
		if !validHomeSIMForwarderConfig(forwarder.Type, forwarder.Config) {
			return false
		}
	}
	return true
}

func validHomeSIMForwarderConfig(kind string, config homeSIMForwarderConfig) bool {
	switch kind {
	case "webhook", "feishu", "wecom":
		return httpsOriginOnly(config.WebhookURL)
	case "dingtalk":
		if !httpsOriginOnly(config.WebhookURL) {
			return false
		}
		return len(config.Secret) <= 128
	case "serverchan", "pushplus":
		if config.CredentialRef == "" || len(config.CredentialRef) > 128 {
			return false
		}
		return !strings.ContainsAny(config.CredentialRef, " \t\r\n=")
	case "nkn":
		if config.CredentialRef == "" || len(config.CredentialRef) > 128 {
			return false
		}
		if strings.ContainsAny(config.CredentialRef, " \t\r\n=") {
			return false
		}
		topic := strings.TrimSpace(config.NKNTopic)
		addresses := nknAddressList(config.NKNAddresses)
		if topic == "" && len(addresses) == 0 {
			return false
		}
		if len(topic) > 64 || strings.ContainsAny(topic, " \t\r\n#") {
			return false
		}
		for _, address := range addresses {
			if len(address) > 96 {
				return false
			}
			if strings.ContainsAny(address, " \t\r\n") {
				return false
			}
		}
		return true
	}
	return false
}

func httpsOriginOnly(raw string) bool {
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return false
	}
	return len(raw) <= 1024
}

func (api *API) homeSIMForwarderPath() (string, bool) {
	directory, ok := api.homeSIMDataDirectory()
	if !ok {
		return "", false
	}
	return filepath.Join(directory, "forwarders.json"), true
}

func (api *API) loadHomeSIMForwarderSettings() homeSIMForwarderSettings {
	current := defaultHomeSIMForwarderSettings()
	path, ok := api.homeSIMForwarderPath()
	if !ok {
		return current
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return current
	}
	var loaded homeSIMForwarderSettings
	if err := json.Unmarshal(data, &loaded); err != nil {
		return defaultHomeSIMForwarderSettings()
	}
	if loaded.Forwarders == nil {
		loaded.Forwarders = []homeSIMForwarder{}
	}
	if loaded.Revision < 1 {
		loaded.Revision = 1
	}
	return loaded
}

func (api *API) homeSIMForwarders(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodPut {
		response.Header().Set("Allow", "GET, PUT")
		writeError(response, 405, "method_not_allowed", "Only GET and PUT are supported", "")
		return
	}
	path, ok := api.homeSIMForwarderPath()
	if !ok {
		writeError(response, 503, "integration_unavailable", "NasSimHub server storage is unavailable", "")
		return
	}
	api.homeSIMForwarderMu.Lock()
	defer api.homeSIMForwarderMu.Unlock()
	current := api.loadHomeSIMForwarderSettings()
	if request.Method == http.MethodPut {
		var next homeSIMForwarderSettings
		if !decodeJSONBody(response, request, &next) {
			return
		}
		if !validHomeSIMForwarderSettings(next) {
			writeError(response, 400, "invalid_argument", "Invalid forwarding channels; check channel type, HTTPS webhook URLs and environment variable names", "forwarders")
			return
		}
		if next.Revision != current.Revision {
			writeError(response, 409, "revision_conflict", "Forwarding channels changed; reload before saving", "revision")
			return
		}
		next.Revision++
		if next.Forwarders == nil {
			next.Forwarders = []homeSIMForwarder{}
		}
		// The browser receives channels with the secret blanked out (only
		// secret_set). Keep the stored secret when the client sends it back
		// empty, otherwise every save would silently wipe it.
		previousSecret := make(map[string]string, len(current.Forwarders))
		for _, existing := range current.Forwarders {
			if existing.Config.Secret != "" {
				previousSecret[existing.ID] = existing.Config.Secret
			}
		}
		for index := range next.Forwarders {
			if next.Forwarders[index].Config.Secret == "" {
				if kept, ok := previousSecret[next.Forwarders[index].ID]; ok {
					next.Forwarders[index].Config.Secret = kept
				}
			}
			next.Forwarders[index].Config.SecretSet = next.Forwarders[index].Config.Secret != ""
		}
		if err := writeHomeSIMJSON(path, next); err != nil {
			api.writeInternalError(response, request, "save SMS forwarding channels", err)
			return
		}
		current = next
	}
	// Never hand stored secrets back to the browser: the 钉钉 signing secret is
	// enough to forge webhook signatures. GET only reports whether one is set.
	sanitized := current
	if len(sanitized.Forwarders) > 0 {
		sanitized.Forwarders = append([]homeSIMForwarder(nil), sanitized.Forwarders...)
		for index := range sanitized.Forwarders {
			if sanitized.Forwarders[index].Config.Secret != "" {
				sanitized.Forwarders[index].Config.Secret = ""
				sanitized.Forwarders[index].Config.SecretSet = true
			}
		}
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, 200, map[string]any{
		"settings":      sanitized,
		"max_channels":  maxForwarderChannels,
		"runtime_ready": true,
	})
}

// homeSIMForwarderTest sends a test message through the requested channel
// without persisting anything. Credential refs resolve from server
// environment variables at send time.
func (api *API) homeSIMForwarderTest(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, 405, "method_not_allowed", "Only POST is supported", "")
		return
	}
	var payload homeSIMForwarder
	if !decodeJSONBody(response, request, &payload) {
		return
	}
	if !forwarderTypes[payload.Type] || !validHomeSIMForwarderConfig(payload.Type, payload.Config) {
		writeError(response, 400, "invalid_argument", "Invalid channel settings for the test send", "channel")
		return
	}
	timeout := forwarderRequestTimeout
	if payload.Type == "nkn" {
		timeout = nknTestTimeout
	}
	ctx, cancel := context.WithTimeout(request.Context(), timeout)
	defer cancel()
	if err := api.deliverForwarderMessage(ctx, api.homeSIMOutboundTransport(request), payload, "NAS猫窝测试推送", "短信转发通道工作正常。"); err != nil {
		writeError(response, 502, "forward_failed", "Test message failed: "+err.Error(), "")
		return
	}
	writeJSON(response, 200, map[string]any{"sent": true})
}

// StartHomeSIMForwarderWorker subscribes to live incoming-SMS events and
// fans each one out to every enabled channel. Channel settings are re-read
// from disk per event so saves take effect immediately.
func (api *API) StartHomeSIMForwarderWorker(ctx context.Context) {
	if api.messageEvents == nil {
		return
	}
	updates, cancel := api.messageEvents.Subscribe()
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-updates:
				if !ok {
					return
				}
				api.dispatchForwarderEvent(event)
			}
		}
	}()
}

func (api *API) dispatchForwarderEvent(event messageevents.IncomingSMS) {
	settings := api.loadHomeSIMForwarderSettings()
	for _, forwarder := range settings.Forwarders {
		if !forwarder.Enabled || !forwarderSubscribesSMS(forwarder) {
			continue
		}
		forwarder := forwarder
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), forwarderRequestTimeout)
			defer cancel()
			title := "短信来自 " + event.Peer
			body := strings.TrimSpace(event.Content)
			if len(body) > 1800 {
				body = body[:1800] + "…"
			}
			// Empty request: Context() falls back to context.Background() so the
			// cellular-line outbound proxy still applies outside HTTP handlers.
			transport := api.homeSIMOutboundTransport(new(http.Request))
			if err := api.deliverForwarderMessage(ctx, transport, forwarder, title, body); err != nil {
				api.logger.Warn("SMS forward failed", "channel", forwarder.Type, "id", forwarder.ID, "error", err)
			}
		}()
	}
}

func forwarderSubscribesSMS(forwarder homeSIMForwarder) bool {
	for _, event := range forwarder.Events {
		if event == "sms" {
			return true
		}
	}
	return false
}

func (api *API) deliverForwarderMessage(ctx context.Context, transport http.RoundTripper, forwarder homeSIMForwarder, title, body string) error {
	content := strings.TrimSpace(title + "\n" + body)
	switch forwarder.Type {
	case "nkn":
		return api.deliverForwarderNKN(forwarder, title, body)
	case "webhook":
		return postForwarderJSON(ctx, transport, forwarder.Config.WebhookURL, "", map[string]any{
			"title": title, "text": body, "body": body,
		})
	case "dingtalk":
		target := forwarder.Config.WebhookURL
		if forwarder.Config.Secret != "" {
			timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
			mac := hmac.New(sha256.New, []byte(forwarder.Config.Secret))
			_, _ = mac.Write([]byte(timestamp + "\n" + forwarder.Config.Secret))
			sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
			separator := "&"
			if !strings.Contains(target, "?") {
				separator = "?"
			}
			target = target + separator + "timestamp=" + timestamp + "&sign=" + url.QueryEscape(sign)
		}
		return postForwarderJSON(ctx, transport, target, "", map[string]any{
			"msgtype": "text",
			"text":    map[string]string{"content": content},
		})
	case "feishu":
		return postForwarderJSON(ctx, transport, forwarder.Config.WebhookURL, "", map[string]any{
			"msg_type": "text",
			"content":  map[string]string{"text": content},
		})
	case "wecom":
		if len(content) > 2048 {
			content = content[:2048]
		}
		return postForwarderJSON(ctx, transport, forwarder.Config.WebhookURL, "", map[string]any{
			"msgtype": "text",
			"text":    map[string]string{"content": content},
		})
	case "serverchan":
		key := strings.TrimSpace(os.Getenv(forwarder.Config.CredentialRef))
		if key == "" {
			return fmt.Errorf("environment variable %s is not set on the server", forwarder.Config.CredentialRef)
		}
		return postForwarderJSON(ctx, transport, "https://sctapi.ftqq.com/"+url.PathEscape(key)+".send", "", map[string]any{
			"title": title, "desp": body,
		})
	case "pushplus":
		token := strings.TrimSpace(os.Getenv(forwarder.Config.CredentialRef))
		if token == "" {
			return fmt.Errorf("environment variable %s is not set on the server", forwarder.Config.CredentialRef)
		}
		return postForwarderJSON(ctx, transport, "https://www.pushplus.plus/send", "", map[string]any{
			"token": token, "title": title, "content": body, "template": "txt",
		})
	}
	return fmt.Errorf("unknown channel type %q", forwarder.Type)
}

func postForwarderJSON(ctx context.Context, transport http.RoundTripper, target string, contentType string, payload map[string]any) error {
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", contentType)
	client := &http.Client{Timeout: forwarderRequestTimeout, Transport: transport}
	forwarderResponse, err := client.Do(request)
	if err != nil {
		return err
	}
	defer forwarderResponse.Body.Close()
	if forwarderResponse.StatusCode < 200 || forwarderResponse.StatusCode >= 300 {
		return fmt.Errorf("channel returned HTTP %s", strconv.Itoa(forwarderResponse.StatusCode))
	}
	return nil
}
