package diagnostics

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

// Every value below is made up for the test. They are shaped like the real
// thing because the redaction is partly by shape.
const (
	advBarkKey       = "advBarkDeviceKeyAAAAAAAAAA"
	advTelegramToken = "123456789:AAadvTelegramBotTokenAAAAAAAAAAAAAAA"
	advServerChanKey = "SCTadvServerChanSendKeyAAAAAAAA"
	advAccessToken   = "advdingtalkaccesstoken0123456789abcdef"
	advWebhookID     = "0f0f0f0f-adv0-4adv-8adv-0f0f0f0f0f0f"
	advBearer        = "advUpdaterBearerToken0123456789"
	advURLPassword   = "advUrlUserinfoPassword"
	advPassword      = "adv-plain-password-value"
	advPSK           = "adv-wifi-psk-value-0001"
	advVerification  = "adv code 000000"
	advPhone         = "+15555550177"
	advSessionCookie = "advSessionCookieValue"
	advPrivateWGKey  = "advWireGuardPrivateKeyValue="
)

func advAllSecrets() []string {
	return []string{
		advBarkKey, advTelegramToken, advServerChanKey, advAccessToken, advWebhookID,
		advBearer, advURLPassword, advPassword, advPSK, advVerification, advPhone,
		advSessionCookie, advPrivateWGKey,
	}
}

func advTransportError(method, target string) error {
	return &url.Error{Op: method, URL: target, Err: errors.New("dial tcp 192.0.2.1:443: connect: connection refused")}
}

// The same redaction must reach both sinks: the buffer behind the diagnostics
// page AND the handler it forwards to, which in production is the process's
// stdout. Before r128 only the buffer was redacted.
func TestAdversarialBothLogSinksAreRedacted(t *testing.T) {
	var stdout bytes.Buffer
	buffer := NewLogBuffer(64)
	logger := slog.New(buffer.Handler(slog.NewJSONHandler(&stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))).
		With("component", "adversarial", "api_token", advBearer)

	// By key.
	logger.Info("settings saved",
		"password", advPassword,
		"wifi_psk", advPSK,
		"passphrase", advPSK,
		"csrf_token", advSessionCookie,
		"cookie", advSessionCookie,
		"private_key_secret", advPrivateWGKey,
		"sender", advPhone,
		"content", advVerification,
		slog.Group("message", "text", advVerification, "peer", advPhone, "line_id", "line-adv"),
	)
	// By value: the key is innocent, the text is not.
	logger.Warn("bark push failed", "error", advTransportError("Post", "https://push.example.test/"+advBarkKey))
	logger.Warn("telegram send failed", "error",
		advTransportError("Post", "https://api.telegram.example.test/bot"+advTelegramToken+"/sendMessage"))
	logger.Warn("SMS forward failed", "channel", "serverchan", "error",
		advTransportError("Post", "https://sct.example.test/"+advServerChanKey+".send"))
	logger.Warn("SMS forward failed", "channel", "dingtalk", "error",
		advTransportError("Post", "https://oapi.example.test/robot/send?access_token="+advAccessToken+"&sign=adv"))
	logger.Warn("SMS forward failed", "channel", "feishu", "error",
		fmt.Errorf("deliver: %w", advTransportError("Post", "https://open.example.test/open-apis/bot/v2/hook/"+advWebhookID)))
	logger.Error("updater refused", "detail", "request sent with Authorization: Bearer "+advBearer)
	logger.Error("proxy failed", "reason", "proxy http://adv-user:"+advURLPassword+"@proxy.example.test:8080 is unreachable")
	// Bound through a derived logger and through a group.
	logger.WithGroup("request").With("authorization", "Bearer "+advBearer).
		Error("upstream failed", "error", advTransportError("Get", "https://push.example.test/"+advBarkKey+"?device="+advBarkKey))
	// In the message itself.
	logger.Error("could not reach https://push.example.test/" + advBarkKey)

	snapshot, err := json.Marshal(buffer.Snapshot(0))
	if err != nil {
		t.Fatal(err)
	}
	for sink, text := range map[string]string{"diagnostics buffer": string(snapshot), "forwarded (stdout) log": stdout.String()} {
		for _, secret := range advAllSecrets() {
			if strings.Contains(text, secret) {
				t.Errorf("the %s contains %q:\n%s", sink, secret, text)
			}
		}
		// What makes the line useful survives: which host failed, which
		// channel, which line, and why.
		for _, kept := range []string{
			"push.example.test", "api.telegram.example.test", "sendMessage", "open-apis/bot/v2/hook",
			"connection refused", "line-adv", "serverchan", "bark push failed", "adversarial",
		} {
			if !strings.Contains(text, kept) {
				t.Errorf("the %s lost %q, which is not a secret:\n%s", sink, kept, text)
			}
		}
	}
	// The forwarded log is still well-formed JSON, one object per line, with
	// groups kept as groups.
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 10 {
		t.Fatalf("forwarded %d lines, want 10", len(lines))
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("forwarded line is not JSON: %v: %s", err, lines[0])
	}
	message, ok := first["message"].(map[string]any)
	if !ok || message["line_id"] != "line-adv" || message["text"] != "[redacted]" {
		t.Fatalf("the forwarded group lost its shape: %v", first["message"])
	}
	if first["password"] != "[redacted]" || first["api_token"] != "[redacted]" {
		t.Fatalf("forwarded keys were not redacted: %v", first)
	}
}

func TestAdversarialScrubSecrets(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"bark key in path",
			`Post "https://api.example.test/` + advBarkKey + `": context deadline exceeded`,
			`Post "https://api.example.test/[redacted]": context deadline exceeded`},
		{"telegram bot token",
			`Post "https://api.example.test/bot` + advTelegramToken + `/sendMessage": EOF`,
			`Post "https://api.example.test/[redacted]/sendMessage": EOF`},
		{"query string",
			`Get "https://api.example.test/send?access_token=` + advAccessToken + `&x=1": EOF`,
			`Get "https://api.example.test/send?[redacted]": EOF`},
		{"userinfo",
			"proxy http://adv-user:" + advURLPassword + "@proxy.example.test:8080/ failed",
			"proxy http://proxy.example.test:8080/ failed"},
		{"websocket", "dial wss://relay.example.test/session/" + advWebhookID + ", retrying",
			"dial wss://relay.example.test/session/[redacted], retrying"},
		{"bare telegram token", "token " + advTelegramToken + " was rejected", "token [redacted] was rejected"},
		{"bearer header", "Authorization: Bearer " + advBearer, "Authorization: Bearer [redacted]"},
		{"short paths are kept", `Get "https://updates.example.test/v2/manifest.json": 404`,
			`Get "https://updates.example.test/v2/manifest.json": 404`},
		{"origin only", "listening on http://192.0.2.10:8080", "listening on http://192.0.2.10:8080"},
		{"no url at all", "the agent socket closed: bearer authentication failed", "the agent socket closed: bearer authentication failed"},
		{"sentence punctuation", "see https://docs.example.test/setup.", "see https://docs.example.test/setup."},
		{"empty", "", ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := ScrubSecrets(test.input)
			if got != test.want {
				t.Fatalf("ScrubSecrets(%q)\n got  %q\n want %q", test.input, got, test.want)
			}
			if again := ScrubSecrets(got); again != got {
				t.Fatalf("scrubbing is not idempotent: %q -> %q", got, again)
			}
		})
	}
}

// Entries imported from the hardware agent go through the same door.
func TestAdversarialExternalEntriesAreScrubbed(t *testing.T) {
	buffer := NewLogBuffer(8)
	buffer.AppendExternal(LogEntry{
		Source:  "agent",
		Message: "push failed for https://push.example.test/" + advBarkKey,
		Fields: map[string]any{
			"error":      `Post "https://push.example.test/` + advBarkKey + `": EOF`,
			"wifi_psk":   advPSK,
			"otp":        advVerification,
			"line_id":    "line-adv",
			"sim.puk":    "00000000",
			"attempt":    3,
			"csrf-token": advSessionCookie,
		},
	})
	encoded, err := json.Marshal(buffer.Snapshot(0))
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{advBarkKey, advPSK, advVerification, advSessionCookie, "00000000"} {
		if strings.Contains(text, secret) {
			t.Errorf("an imported entry kept %q: %s", secret, text)
		}
	}
	for _, kept := range []string{"line-adv", "push.example.test", `"attempt":3`} {
		if !strings.Contains(text, kept) {
			t.Errorf("an imported entry lost %q: %s", kept, text)
		}
	}
}

// What the scrubber does NOT do, so nobody reads the tests above as a promise
// it cannot keep: a secret under an innocent key, in free text, with no URL,
// token or header shape around it, is not recognised. The defence against
// that is not logging it, which is what the httpapi adversarial tests check.
func TestAdversarialScrubberLimitIsDocumented(t *testing.T) {
	const shapeless = "adv-shapeless-secret"
	if got := ScrubSecrets("the value was " + shapeless); !strings.Contains(got, shapeless) {
		t.Fatalf("a shapeless value is now scrubbed (%q): update this test and the audit's limits section", got)
	}
	// A path segment shorter than sixteen characters is kept, key or not.
	if got := ScrubSecrets("https://push.example.test/shortkey123"); !strings.Contains(got, "shortkey123") {
		t.Fatalf("a short path segment is now scrubbed (%q): update this test and the audit's limits section", got)
	}
}
