// SPDX-License-Identifier: AGPL-3.0-only
package forwarding

import (
	"bufio"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/secretbox"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestEveryHTTPChannelUsesBoundedRealLocalRequestsAndApplicationACK(t *testing.T) {
	for _, kind := range Catalog {
		if kind.Type == "email" || kind.Type == "sms" || kind.Type == "socket" {
			continue
		}
		t.Run(kind.Type, func(t *testing.T) {
			var requests atomic.Int32
			var reject atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				raw, err := io.ReadAll(io.LimitReader(r.Body, 65537))
				if err != nil || len(raw) > 65536 {
					t.Error("unbounded body")
				}
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "gettoken") || strings.Contains(r.URL.Path, "tenant_access_token") {
					fmt.Fprint(w, `{"errcode":0,"code":0,"access_token":"synthetic-token","tenant_access_token":"synthetic-token"}`)
					return
				}
				if reject.Load() {
					fmt.Fprint(w, `{"errcode":1,"code":1,"ok":false}`)
					return
				}
				switch kind.Type {
				case "bark", "pushplus":
					fmt.Fprint(w, `{"code":200}`)
				case "serverchan", "feishu", "feishu_app":
					fmt.Fprint(w, `{"code":0}`)
				case "dingtalk", "dingtalk_app", "wecom", "wecom_app":
					fmt.Fprint(w, `{"errcode":0}`)
				case "telegram":
					fmt.Fprint(w, `{"ok":true}`)
				case "gotify":
					fmt.Fprint(w, `{"id":1}`)
				default:
					w.WriteHeader(204)
				}
			}))
			defer server.Close()
			target, _ := url.Parse(server.URL)
			ch := Channel{ID: "test", Type: kind.Type, Enabled: true, Params: map[string]string{"agent_id": "1", "to": "synthetic-user", "chat_id": "1", "receive_id_type": "open_id", "server": server.URL}, Secrets: map[string]Secret{}}
			for _, key := range kind.Secrets {
				if key == "headers" || key == "signing_key" || key == "encryption_key" {
					continue
				}
				ch.Secrets[key] = Secret{Env: "TEST_" + key}
			}
			d := &Dispatcher{LookupEnv: func(key string) (string, bool) {
				if key == "TEST_url" {
					return server.URL, true
				}
				return "synthetic-value", true
			}, Transport: func(context.Context) http.RoundTripper {
				return transportFunc(func(r *http.Request) (*http.Response, error) {
					copy := r.Clone(r.Context())
					u := *r.URL
					copy.URL = &u
					copy.URL.Scheme = target.Scheme
					copy.URL.Host = target.Host
					return http.DefaultTransport.RoundTrip(copy)
				})
			}}
			if err := d.Send(context.Background(), ch, Rendered{Title: "synthetic title", Body: "synthetic body"}, "id"); err != nil {
				t.Fatal(err)
			}
			want := int32(1)
			if strings.HasSuffix(kind.Type, "_app") {
				want = 2
			}
			if requests.Load() != want {
				t.Fatalf("requests=%d", requests.Load())
			}
			if kind.Type != "webhook" {
				reject.Store(true)
				if !errors.Is(d.Send(context.Background(), ch, Rendered{Title: "test"}, "id"), ErrDelivery) {
					t.Fatal("HTTP200 provider error treated as success")
				}
			}
		})
	}
}
func TestBarkCBCEncryptionAndSecretIsolation(t *testing.T) {
	key := "0123456789abcdef"
	var decrypted atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "private synthetic content") {
			t.Error("unencrypted Bark body")
		}
		var envelope map[string]string
		json.Unmarshal(raw, &envelope)
		body, err := base64.StdEncoding.DecodeString(envelope["ciphertext"])
		if err != nil || len(body)%16 != 0 {
			t.Error("invalid ciphertext")
			return
		}
		block, _ := aes.NewCipher([]byte(key))
		cipher.NewCBCDecrypter(block, []byte(envelope["iv"])).CryptBlocks(body, body)
		if strings.Contains(string(body), "private synthetic content") {
			decrypted.Store(true)
		}
		fmt.Fprint(w, `{"code":200}`)
	}))
	defer server.Close()
	ch := Channel{ID: "bark", Type: "bark", Params: map[string]string{"server": server.URL, "encryption": "aes-cbc"}, Secrets: map[string]Secret{"key": {Env: "DEVICE"}, "encryption_key": {Env: "AES_KEY"}}}
	d := &Dispatcher{LookupEnv: func(name string) (string, bool) {
		if name == "AES_KEY" {
			return key, true
		}
		return "synthetic-device-key", true
	}}
	if err := d.Send(context.Background(), ch, Rendered{Body: "private synthetic content"}, "id"); err != nil || !decrypted.Load() {
		t.Fatal("encryption", err)
	}
	box, _ := secretbox.New(make([]byte, 32))
	c := DefaultConfig()
	c.Channels = []Channel{{ID: "secret", Type: "webhook", Params: map[string]string{}, Secrets: map[string]Secret{"url": {Value: server.URL + "/private-key"}}}}
	sealed, err := SealConfig(c, DefaultConfig(), box)
	if err != nil {
		t.Fatal(err)
	}
	private, _ := json.Marshal(sealed)
	public, _ := json.Marshal(sealed.Public())
	if strings.Contains(string(private), "private-key") || strings.Contains(string(public), "ciphertext") || strings.Contains(string(public), "nonce") {
		t.Fatal("secret escaped")
	}
	d.Secrets = box
	if value, err := d.secret(sealed.Channels[0], "url"); err != nil || !strings.HasSuffix(value, "/private-key") {
		t.Fatal("sealed secret failed")
	}
	publicConfig := sealed.Public()
	kept, err := SealConfig(publicConfig, sealed, box)
	if err != nil || len(kept.Channels[0].Secrets["url"].Ciphertext) == 0 {
		t.Fatal("hidden secret lost")
	}
	copied := sealed.Channels[0]
	copied.ID = "another"
	if _, err = d.secret(copied, "url"); !errors.Is(err, ErrDenied) {
		t.Fatal("secret AD not bound to channel")
	}
}
func TestRedirectsAndOversizedRepliesRefuseWithoutCredentialForwarding(t *testing.T) {
	var forwarded atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }))
	defer source.Close()
	d := &Dispatcher{}
	if _, err := d.request(context.Background(), "POST", source.URL, map[string]string{"secret": "synthetic"}, nil); !errors.Is(err, ErrDelivery) || forwarded.Load() != 0 {
		t.Fatal("redirect followed")
	}
	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, strings.Repeat("x", 40000)) }))
	defer large.Close()
	if _, err := d.request(context.Background(), "GET", large.URL, nil, nil); !errors.Is(err, ErrDelivery) {
		t.Fatal("oversized reply accepted")
	}
}
func TestSMTPImplicitTLSAcceptedAndPlaintextRefused(t *testing.T) {
	certServer := httptest.NewTLSServer(nil)
	certificate := certServer.TLS.Certificates[0]
	pool := x509.NewCertPool()
	pool.AddCert(certServer.Certificate())
	certServer.Close()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := tls.NewListener(raw, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	defer listener.Close()
	done := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- "accept failed"
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(conn)
		fmt.Fprint(conn, "220 test ESMTP\r\n")
		message := ""
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				done <- message
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				fmt.Fprint(conn, "250 test\r\n")
			case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
				fmt.Fprint(conn, "250 OK\r\n")
			case strings.HasPrefix(line, "DATA"):
				fmt.Fprint(conn, "354 Continue\r\n")
				for {
					part, e := reader.ReadString('\n')
					if e != nil || part == ".\r\n" {
						break
					}
					message += part
				}
				fmt.Fprint(conn, "250 Accepted\r\n")
			case strings.HasPrefix(line, "QUIT"):
				fmt.Fprint(conn, "221 Bye\r\n")
				done <- message
				return
			default:
				fmt.Fprint(conn, "500 Unsupported\r\n")
			}
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	ch := Channel{Type: "email", Params: map[string]string{"host": host, "port": port, "tls": "implicit", "from": "from@example.test", "to": "to@example.test"}, Secrets: map[string]Secret{}}
	d := &Dispatcher{tlsRoots: pool}
	if err = d.Send(context.Background(), ch, Rendered{Title: "中文测试", Body: "synthetic SMTP body"}, "id"); err != nil {
		t.Fatal(err)
	}
	message := <-done
	if !strings.Contains(message, base64.StdEncoding.EncodeToString([]byte("synthetic SMTP body"))) {
		t.Fatal("missing MIME body")
	}
	ch.Params["tls"] = "none"
	if !errors.Is(d.Send(context.Background(), ch, Rendered{}, "id"), ErrDenied) {
		t.Fatal("plaintext SMTP permitted")
	}
}
func TestSocketTCPUDPAndMQTTQoS1(t *testing.T) {
	t.Run("tcp", func(t *testing.T) {
		listener, _ := net.Listen("tcp", "127.0.0.1:0")
		defer listener.Close()
		done := make(chan string, 1)
		go func() {
			c, err := listener.Accept()
			if err != nil {
				done <- ""
				return
			}
			defer c.Close()
			b, _ := io.ReadAll(c)
			done <- string(b)
		}()
		host, port, _ := net.SplitHostPort(listener.Addr().String())
		d := &Dispatcher{}
		if err := d.Send(context.Background(), Channel{Type: "socket", Params: map[string]string{"protocol": "tcp", "host": host, "port": port}}, Rendered{Title: "test", Body: "TCP"}, "id"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(<-done, "TCP") {
			t.Fatal("no TCP bytes")
		}
	})
	t.Run("udp", func(t *testing.T) {
		listener, _ := net.ListenPacket("udp", "127.0.0.1:0")
		defer listener.Close()
		host, port, _ := net.SplitHostPort(listener.LocalAddr().String())
		d := &Dispatcher{}
		if err := d.Send(context.Background(), Channel{Type: "socket", Params: map[string]string{"protocol": "udp", "host": host, "port": port}}, Rendered{Body: "UDP"}, "id"); err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 100)
		listener.SetReadDeadline(time.Now().Add(time.Second))
		n, _, err := listener.ReadFrom(b)
		if err != nil || string(b[:n]) != "UDP" {
			t.Fatal("no UDP bytes", err)
		}
	})
	t.Run("mqtt", func(t *testing.T) {
		listener, _ := net.Listen("tcp", "127.0.0.1:0")
		defer listener.Close()
		done := make(chan bool, 1)
		go func() {
			c, err := listener.Accept()
			if err != nil {
				done <- false
				return
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(5 * time.Second))
			header, _, err := mqttRead(c)
			if err != nil || header != 0x10 {
				done <- false
				return
			}
			c.Write([]byte{0x20, 2, 0, 0})
			header, payload, err := mqttRead(c)
			if err != nil || header != 0x32 || !strings.Contains(string(payload), "MQTT body") {
				done <- false
				return
			}
			c.Write([]byte{0x40, 2, 0, 1})
			header, _, err = mqttRead(c)
			done <- err == nil && header == 0xe0
		}()
		host, port, _ := net.SplitHostPort(listener.Addr().String())
		d := &Dispatcher{}
		if err := d.Send(context.Background(), Channel{Type: "socket", Params: map[string]string{"protocol": "mqtt", "host": host, "port": port, "topic": "synthetic/messages"}}, Rendered{Body: "MQTT body"}, "id"); err != nil {
			t.Fatal(err)
		}
		if !<-done {
			t.Fatal("MQTT ACK sequence")
		}
	})
}
func TestTemplatesRejectExecutionAndOutputExpansion(t *testing.T) {
	for _, text := range []string{`{{printf "%999999999s" .Body}}`, `{{range .Body}}x{{end}}`, `{{.Unknown}}`} {
		c := DefaultConfig()
		c.Templates[0].Body = text
		if c.Validate() == nil {
			t.Fatal("unsafe template accepted", text)
		}
	}
	c := DefaultConfig()
	c.Templates[0].Body = strings.Repeat("{{.Body}}", 100)
	if _, err := c.Render(Rule{TemplateID: "default"}, Event{Body: strings.Repeat("x", 1000)}); err == nil {
		t.Fatal("unbounded rendering")
	}
}

func TestSMSUsesOriginalCallbackWithStableRequestIdentity(t *testing.T) {
	var calls int
	ctx := ContextWithSenderOwner(context.Background(), "owner")
	d := &Dispatcher{SMS: func(ctx context.Context, line, to, body, id string) error {
		calls++
		if SenderOwner(ctx) != "owner" || line != "line-A" || to != "+15555550100" || body != "synthetic" || id != "forwarding_42" {
			t.Fatal("SMS authority or request binding changed")
		}
		return nil
	}}
	ch := Channel{Type: "sms", Params: map[string]string{"line_id": "line-A", "to": "+15555550100"}}
	for i := 0; i < 2; i++ {
		if err := d.Send(ctx, ch, Rendered{Body: "synthetic"}, taskRequestID(42)); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal("callback was not exercised")
	}
}
