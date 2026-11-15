// SPDX-License-Identifier: AGPL-3.0-only
package forwarding

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
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

var ErrDelivery = errors.New("delivery_failed")

func (d *Dispatcher) request(ctx context.Context, method, target string, payload any, headers map[string]string) (map[string]any, error) {
	parsed, err := url.Parse(target)
	if err != nil || !slicesURL(parsed) {
		return nil, ErrDenied
	}
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil || len(raw) > 65536 {
			return nil, ErrDenied
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, ErrDenied
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	for key, value := range headers {
		if strings.ContainsAny(key+value, "\r\n") || strings.EqualFold(key, "Host") || strings.EqualFold(key, "Content-Length") {
			return nil, ErrDenied
		}
		req.Header.Set(key, value)
	}
	var transport http.RoundTripper
	if d.Transport != nil {
		transport = d.Transport(ctx)
	}
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, ErrDelivery
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32769))
	if err != nil || len(raw) > 32768 || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ErrDelivery
	}
	result := map[string]any{}
	_ = json.Unmarshal(raw, &result)
	return result, nil
}
func slicesURL(u *url.URL) bool {
	return u != nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}
func codeOK(result map[string]any, key string, want float64) error {
	n, ok := result[key].(float64)
	if !ok || n != want {
		return ErrDelivery
	}
	return nil
}
func (d *Dispatcher) Send(ctx context.Context, ch Channel, m Rendered, requestID string) error {
	p := ch.Params
	text := strings.TrimSpace(m.Title + "\n" + m.Body)
	secret := func(key string) (string, error) { return d.secret(ch, key) }
	post := func(target string, body any, header map[string]string) (map[string]any, error) {
		return d.request(ctx, "POST", target, body, header)
	}
	switch ch.Type {
	case "webhook", "dingtalk", "wecom", "feishu":
		target, err := secret("url")
		if err != nil {
			return err
		}
		var body any = map[string]string{"title": m.Title, "body": m.Body}
		headers := map[string]string{}
		switch ch.Type {
		case "webhook":
			if _, exists := ch.Secrets["headers"]; exists && (ch.Secrets["headers"].Env != "" || len(ch.Secrets["headers"].Ciphertext) > 0) {
				raw, err := secret("headers")
				if err != nil {
					return err
				}
				if json.Unmarshal([]byte(raw), &headers) != nil || len(headers) > 32 {
					return ErrDenied
				}
			}
		case "dingtalk":
			body = map[string]any{"msgtype": "text", "text": map[string]string{"content": text}}
			s := ch.Secrets["signing_key"]
			if s.Env != "" || len(s.Ciphertext) > 0 {
				key, err := secret("signing_key")
				if err != nil {
					return err
				}
				timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
				mac := hmac.New(sha256.New, []byte(key))
				mac.Write([]byte(timestamp + "\n" + key))
				u, err := url.Parse(target)
				if err != nil {
					return ErrDenied
				}
				q := u.Query()
				q.Set("timestamp", timestamp)
				q.Set("sign", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
				u.RawQuery = q.Encode()
				target = u.String()
			}
		case "wecom":
			body = map[string]any{"msgtype": "text", "text": map[string]string{"content": text}}
		case "feishu":
			payload := map[string]any{"msg_type": "text", "content": map[string]string{"text": text}}
			s := ch.Secrets["signing_key"]
			if s.Env != "" || len(s.Ciphertext) > 0 {
				key, err := secret("signing_key")
				if err != nil {
					return err
				}
				timestamp := strconv.FormatInt(time.Now().Unix(), 10)
				mac := hmac.New(sha256.New, []byte(timestamp+"\n"+key))
				payload["timestamp"] = timestamp
				payload["sign"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))
			}
			body = payload
		}
		result, err := post(target, body, headers)
		if err != nil {
			return err
		}
		if ch.Type == "dingtalk" || ch.Type == "wecom" {
			return codeOK(result, "errcode", 0)
		}
		if ch.Type == "feishu" {
			if _, ok := result["code"]; ok {
				return codeOK(result, "code", 0)
			}
			return codeOK(result, "StatusCode", 0)
		}
		return nil
	case "bark":
		key, err := secret("key")
		if err != nil {
			return err
		}
		server := p["server"]
		if server == "" {
			server = "https://api.day.app"
		}
		body := map[string]any{"device_key": key, "title": m.Title, "body": m.Body}
		for _, field := range []string{"group", "sound"} {
			if p[field] != "" {
				body[field] = p[field]
			}
		}
		if p["encryption"] != "" && p["encryption"] != "none" {
			if p["encryption"] != "aes-cbc" {
				return ErrDenied
			}
			aesKey, err := secret("encryption_key")
			if err != nil {
				return err
			}
			block, err := aes.NewCipher([]byte(aesKey))
			if err != nil {
				return ErrDenied
			}
			raw, _ := json.Marshal(body)
			padding := aes.BlockSize - len(raw)%aes.BlockSize
			raw = append(raw, bytes.Repeat([]byte{byte(padding)}, padding)...)
			ivBytes := make([]byte, 8)
			if _, err = rand.Read(ivBytes); err != nil {
				return ErrDelivery
			}
			iv := hex.EncodeToString(ivBytes)
			cipher.NewCBCEncrypter(block, []byte(iv)).CryptBlocks(raw, raw)
			body = map[string]any{"device_key": key, "ciphertext": base64.StdEncoding.EncodeToString(raw), "iv": iv}
		}
		result, err := post(strings.TrimRight(server, "/")+"/push", body, nil)
		if err != nil {
			return err
		}
		return codeOK(result, "code", 200)
	case "serverchan":
		key, err := secret("key")
		if err != nil {
			return err
		}
		result, err := post("https://sctapi.ftqq.com/"+url.PathEscape(key)+".send", map[string]string{"title": m.Title, "desp": m.Body}, nil)
		if err != nil {
			return err
		}
		return codeOK(result, "code", 0)
	case "pushplus":
		token, err := secret("token")
		if err != nil {
			return err
		}
		result, err := post("https://www.pushplus.plus/send", map[string]string{"token": token, "title": m.Title, "content": m.Body, "template": "txt", "topic": p["topic"]}, nil)
		if err != nil {
			return err
		}
		return codeOK(result, "code", 200)
	case "gotify":
		token, err := secret("token")
		if err != nil || p["server"] == "" {
			return ErrDenied
		}
		priority, err := integer(p, "priority", 5)
		if err != nil || priority < 0 || priority > 10 {
			return ErrDenied
		}
		result, err := post(strings.TrimRight(p["server"], "/")+"/message", map[string]any{"title": m.Title, "message": m.Body, "priority": priority}, map[string]string{"X-Gotify-Key": token})
		if err != nil {
			return err
		}
		if _, ok := result["id"].(float64); !ok {
			return ErrDelivery
		}
		return nil
	case "telegram":
		token, err := secret("token")
		if err != nil || p["chat_id"] == "" {
			return ErrDenied
		}
		result, err := post("https://api.telegram.org/bot"+url.PathEscape(token)+"/sendMessage", map[string]any{"chat_id": p["chat_id"], "text": text}, nil)
		if err != nil {
			return err
		}
		if result["ok"] != true {
			return ErrDelivery
		}
		return nil
	case "wecom_app":
		corp, err := secret("corp_id")
		if err != nil {
			return err
		}
		key, err := secret("corp_secret")
		if err != nil {
			return err
		}
		agent, err := integer(p, "agent_id", 0)
		if err != nil || agent <= 0 || p["to"] == "" {
			return ErrDenied
		}
		auth, err := d.request(ctx, "GET", "https://qyapi.weixin.qq.com/cgi-bin/gettoken?corpid="+url.QueryEscape(corp)+"&corpsecret="+url.QueryEscape(key), nil, nil)
		if err != nil {
			return err
		}
		token, _ := auth["access_token"].(string)
		if codeOK(auth, "errcode", 0) != nil || token == "" {
			return ErrDelivery
		}
		result, err := post("https://qyapi.weixin.qq.com/cgi-bin/message/send?access_token="+url.QueryEscape(token), map[string]any{"touser": p["to"], "agentid": agent, "msgtype": "text", "text": map[string]string{"content": text}}, nil)
		if err != nil {
			return err
		}
		return codeOK(result, "errcode", 0)
	case "dingtalk_app":
		app, err := secret("app_key")
		if err != nil {
			return err
		}
		key, err := secret("app_secret")
		if err != nil {
			return err
		}
		agent, err := integer(p, "agent_id", 0)
		if err != nil || agent <= 0 || p["to"] == "" {
			return ErrDenied
		}
		auth, err := d.request(ctx, "GET", "https://oapi.dingtalk.com/gettoken?appkey="+url.QueryEscape(app)+"&appsecret="+url.QueryEscape(key), nil, nil)
		if err != nil {
			return err
		}
		token, _ := auth["access_token"].(string)
		if codeOK(auth, "errcode", 0) != nil || token == "" {
			return ErrDelivery
		}
		result, err := post("https://oapi.dingtalk.com/topapi/message/corpconversation/asyncsend_v2?access_token="+url.QueryEscape(token), map[string]any{"agent_id": agent, "userid_list": p["to"], "msg": map[string]any{"msgtype": "text", "text": map[string]string{"content": text}}}, nil)
		if err != nil {
			return err
		}
		return codeOK(result, "errcode", 0)
	case "feishu_app":
		app, err := secret("app_id")
		if err != nil {
			return err
		}
		key, err := secret("app_secret")
		if err != nil {
			return err
		}
		idType := p["receive_id_type"]
		if idType != "open_id" && idType != "user_id" && idType != "chat_id" && idType != "email" {
			return ErrDenied
		}
		if p["to"] == "" {
			return ErrDenied
		}
		auth, err := post("https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal", map[string]string{"app_id": app, "app_secret": key}, nil)
		if err != nil {
			return err
		}
		token, _ := auth["tenant_access_token"].(string)
		if codeOK(auth, "code", 0) != nil || token == "" {
			return ErrDelivery
		}
		result, err := post("https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type="+idType, map[string]string{"receive_id": p["to"], "msg_type": "text", "content": jsonText(map[string]string{"text": text})}, map[string]string{"Authorization": "Bearer " + token})
		if err != nil {
			return err
		}
		return codeOK(result, "code", 0)
	case "sms":
		if d.SMS == nil || p["line_id"] == "" || p["to"] == "" {
			return ErrDenied
		}
		return d.SMS(ctx, p["line_id"], p["to"], text, requestID)
	case "email":
		return d.sendEmail(ctx, ch, m)
	case "socket":
		return d.sendSocket(ctx, ch, text, requestID)
	}
	return fmt.Errorf("%w: unsupported_channel", ErrDenied)
}
