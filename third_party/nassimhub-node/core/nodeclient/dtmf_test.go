package nodeclient

import (
	"context"
	"encoding/json"
	"github.com/human-agent65535/nassimhub-node/core/transport"
	"github.com/human-agent65535/nassimhub-node/proto"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDTMFSelectedCallBearerAndNoUncertainReplay(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writes++
		if r.Method != "POST" || r.URL.Path != "/v1/calls/mm-7/dtmf" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("incorrect DTMF route or authentication")
		}
		var request proto.DTMFRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.RequestID != "one" || request.Digits != "1#" {
			t.Error("incorrect DTMF payload")
		}
		// A server failure may occur after a tone was accepted. Never resend it.
		http.Error(w, "uncertain", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	selector, err := transport.New(transport.Options{DeviceID: "test-node", Endpoints: []transport.Endpoint{{Type: proto.ConnectionUSB, BaseURL: server.URL}}, Probe: func(context.Context, transport.Endpoint) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{selector: selector, http: server.Client(), now: time.Now, session: proto.Session{Token: "test-token", ExpiresAt: time.Now().Add(time.Hour)}}
	if _, err = client.SendDTMF(context.Background(), "mm-7", proto.DTMFRequest{RequestID: "one", Digits: "1#"}); err == nil {
		t.Fatal("server failure accepted")
	}
	if writes != 1 {
		t.Fatalf("uncertain attempt sent %d times", writes)
	}
	if _, err = client.SendDTMF(context.Background(), "mm-7/../8", proto.DTMFRequest{RequestID: "two", Digits: "1"}); err == nil {
		t.Fatal("unsafe call id accepted")
	}
	if writes != 1 {
		t.Fatal("unsafe call reached network")
	}
}
