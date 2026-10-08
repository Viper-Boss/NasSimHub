package nodeclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/human-agent65535/nassimhub-node/core/transport"
	"github.com/human-agent65535/nassimhub-node/proto"
)

func TestOpenMediaKeepsUpgradedStreamAfterHandshake(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/calls/mm-0123456789abcdef0123456789abcdef-7/media" ||
			r.Header.Get("Authorization") != "Bearer test-token" ||
			r.Header.Get("Upgrade") != "modemdeck-media-v2" {
			http.Error(w, "unexpected media request", http.StatusBadRequest)
			return
		}
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
			"Connection: Upgrade\r\nUpgrade: modemdeck-media-v2\r\n" +
			"ModemDeck-Media-Version: 2\r\nModemDeck-Media-Encoding: pcm\r\n" +
			"ModemDeck-Media-Resolution: s16le\r\nModemDeck-Media-Rate: 16000\r\n" +
			"ModemDeck-Media-Channels: 1\r\nModemDeck-Media-Frame-Duration-Ms: 20\r\n" +
			"ModemDeck-Media-Frame-Bytes: 640\r\n\r\n")
		if err := rw.Flush(); err != nil {
			t.Error(err)
			return
		}
		var data [4]byte
		if _, err := io.ReadFull(rw, data[:]); err != nil {
			return
		}
		_, _ = rw.Write(data[:])
		_ = rw.Flush()
	}))
	defer server.Close()
	selector, err := transport.New(transport.Options{
		DeviceID:  "test-node",
		Endpoints: []transport.Endpoint{{Type: proto.ConnectionUSB, BaseURL: server.URL}},
		Probe:     func(context.Context, transport.Endpoint) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{
		selector: selector,
		http:     &http.Client{Timeout: 50 * time.Millisecond, Transport: http.DefaultTransport},
		now:      time.Now,
		session:  proto.Session{Token: "test-token", ExpiresAt: time.Now().Add(time.Hour)},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	stream, err := client.OpenMedia(ctx, "mm-0123456789abcdef0123456789abcdef-7")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	defer stream.Close()
	time.Sleep(75 * time.Millisecond)
	if _, err := stream.Write([]byte("PING")); err != nil {
		t.Fatal(err)
	}
	var reply [4]byte
	if _, err := io.ReadFull(stream, reply[:]); err != nil || string(reply[:]) != "PING" {
		t.Fatalf("upgraded stream = %q, %v", reply, err)
	}
}

func TestOpenMediaRejectsInvalidCallBeforeNetwork(t *testing.T) {
	client := &Client{}
	if _, err := client.OpenMedia(context.Background(), "mm-7/../8"); err == nil {
		t.Fatal("accepted an unsafe call ID")
	}
}
