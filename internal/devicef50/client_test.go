package devicef50

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newZTEFixture spins up a fake ZTE web backend implementing the goform
// handshake exactly like the stock F50 firmware.
func newZTEFixture(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	passwordHash := sha256Upper(sha256Upper("testpass") + "ld-token-1")
	adToken := sha256Upper(sha256Upper("wa-1.0cr-2.0") + "rd-token-1")

	mux.HandleFunc("/goform/goform_get_cmd_process", func(writer http.ResponseWriter, request *http.Request) {
		cmd := request.URL.Query().Get("cmd")
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case cmd == "LD":
			_ = json.NewEncoder(writer).Encode(map[string]string{"LD": "ld-token-1"})
		case cmd == "RD" || contains(cmd, "RD"):
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"RD":               "rd-token-1",
				"wa_inner_version": "wa-1.0",
				"cr_version":       "cr-2.0",
				"Language":         "zh_CN",
			})
		case contains(cmd, "sms_data_total"):
			if request.Header.Get("Cookie") == "" {
				writer.WriteHeader(http.StatusFound)
				return
			}
			content := base64.StdEncoding.EncodeToString([]byte("家里宽带白天有人吗"))
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"messages": []map[string]any{
					{"id": "7", "number": "13901898834", "content": content, "date": "26;09;06;14;20;00", "tag": "0"},
					{"id": "6", "number": "13801662210", "content": "aGVsbG8=", "date": "26;09;06;14;00;00", "tag": "1"},
				},
			})
		default:
			if request.Header.Get("Cookie") == "" {
				writer.WriteHeader(http.StatusFound)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"network_type":        "n78",
				"network_provider":    "CHINA MOBILE",
				"signalbar":           "4",
				"lte_rsrp":            "-87",
				"lte_rsrq":            "-10",
				"lte_snr":             "13",
				"wan_active_band":     "n78",
				"realtime_rx_thrpt":   "12.5KB/s",
				"monthly_rx_bytes":    "19964354560",
				"monthly_tx_bytes":    "2040109466",
				"monthly_clear_day":   "1",
				"temperature":         "41",
				"cpu_temp":            "44",
				"wifi_access_sta_num": "2",
				"sms_unread_num":      "1",
				"wa_inner_version":    "wa-1.0",
				"cr_version":          "cr-2.0",
			})
		}
	})

	mux.HandleFunc("/goform/goform_set_cmd_process", func(writer http.ResponseWriter, request *http.Request) {
		_ = request.ParseForm()
		goformID := request.Form.Get("goformId")
		writer.Header().Set("Content-Type", "application/json")
		switch goformID {
		case "LOGIN":
			if request.Form.Get("password") == passwordHash {
				http.SetCookie(writer, &http.Cookie{Name: "stok", Value: "session-abc"})
				_ = json.NewEncoder(writer).Encode(map[string]any{"result": "0"})
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"result": "failure"})
		case "SEND_SMS":
			if request.Form.Get("AD") != adToken {
				_ = json.NewEncoder(writer).Encode(map[string]any{"result": "failure"})
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"result": "0"})
		case "SET_MSG_READ":
			if request.Form.Get("AD") != adToken {
				_ = json.NewEncoder(writer).Encode(map[string]any{"result": "failure"})
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"result": "0"})
		default:
			_ = json.NewEncoder(writer).Encode(map[string]any{"result": "failure"})
		}
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && (haystack == needle ||
		len(haystack) > 0 && stringContains(haystack, needle))
}

func stringContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestClientFullFlow(t *testing.T) {
	server := newZTEFixture(t)
	client := New(server.URL, "admin", "testpass")

	status, err := client.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.NetworkType != "n78" || status.Provider != "CHINA MOBILE" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.RSRP != "-87" || status.Signalbar != "4" {
		t.Fatalf("signal fields wrong: %+v", status)
	}
	if status.MonthlyRxBytes != "19964354560" || status.BillingDay != "1" {
		t.Fatalf("traffic fields wrong: %+v", status)
	}

	messages, err := client.SMSList(0)
	if err != nil {
		t.Fatalf("SMSList: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[0].Content != "家里宽带白天有人吗" {
		t.Fatalf("base64 content not decoded: %q", messages[0].Content)
	}
	if messages[1].Content != "hello" {
		t.Fatalf("simple base64 content wrong: %q", messages[1].Content)
	}

	if err := client.MarkRead([]string{"7"}); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if err := client.SendSMS("139 0189 8834", "你好，F50"); err != nil {
		t.Fatalf("SendSMS: %v", err)
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	server := newZTEFixture(t)
	client := New(server.URL, "admin", "wrongpass")
	if _, err := client.Status(); err == nil {
		t.Fatal("wrong password should fail")
	}
}
