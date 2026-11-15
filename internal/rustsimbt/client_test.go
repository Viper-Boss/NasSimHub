// SPDX-License-Identifier: AGPL-3.0-only
package rustsimbt

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/airsimgw"
	"github.com/human-agent65535/modemdeck/internal/airsimproto"
)

type authority struct{ airsimgw.Backend }

func (authority) Authorize(context.Context, airsimgw.Identity) error { return nil }

func TestEnrollmentCallsAndPCMOverTwoApplicationStreams(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	window, err := airsimproto.NewPairingSession(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	type fake struct {
		key     []byte
		current bool
	}
	stick := fake{}
	dial := func(ctx context.Context, channel byte) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			if channel == audioChannel {
				reader := bufio.NewReader(server)
				line, e := reader.ReadString('\n')
				if e != nil {
					t.Error(e)
					return
				}
				p, e := airsimproto.ParsePCMPreface(line)
				if e != nil || !airsimproto.VerifySignature(stick.key, p.Signature, airsimproto.PCMCanonical(p)) {
					t.Error("PCM preface invalid")
					return
				}
				if e := binary.Write(server, binary.BigEndian, uint32(8000)); e != nil {
					t.Error(e)
					return
				}
				if _, e := io.WriteString(server, airsimproto.PCMReady); e != nil {
					t.Error(e)
					return
				}
				frame := make([]byte, 320)
				frame[0] = 0x55
				if _, e := server.Write(frame); e != nil {
					t.Error(e)
					return
				}
				if _, e := io.ReadFull(reader, frame); e != nil || frame[0] != 0xaa {
					t.Error("microphone frame missing")
				}
				return
			}
			if channel != controlChannel {
				t.Error("illegal channel")
				return
			}
			reader := bufio.NewReader(server)
			selector, e := reader.ReadByte()
			if e != nil {
				t.Error(e)
				return
			}
			request, e := http.ReadRequest(reader)
			if e != nil {
				t.Error(e)
				return
			}
			body, e := io.ReadAll(request.Body)
			if e != nil {
				t.Error(e)
				return
			}
			path := request.URL.RequestURI()
			nonce := request.Header.Get(airsimproto.HeaderNonce)
			if selector == 1 {
				ts, e := strconv.ParseInt(request.Header.Get(airsimproto.HeaderTimestamp), 10, 64)
				if e != nil || !airsimproto.VerifySignature(stick.key, request.Header.Get(airsimproto.HeaderSignature), airsimproto.Canonical(request.Method, path, body, ts, nonce)) {
					t.Error("bad signature")
					return
				}
			}
			var response any
			switch path {
			case "/pair/v1/info":
				if selector != 2 {
					t.Error("pairing used wrong route")
				}
				response = map[string]string{"session_id": window.ID, "server_public_key": airsimproto.Base64URL(window.PublicKey())}
			case "/pair/v1/claim":
				var input map[string]any
				if json.Unmarshal(body, &input) != nil {
					t.Error("bad claim")
					return
				}
				peer, _ := input["client_public_key"].(string)
				blob, _ := input["sealed_registration"].(string)
				key, _ := airsimproto.DecodeBase64URL(peer)
				sealed, _ := airsimproto.DecodeBase64URL(blob)
				plain, e := window.Open(key, sealed, time.Now())
				var reg map[string]string
				if e != nil || json.Unmarshal(plain, &reg) != nil || reg["device_id"] != "NasSimHub-Bluetooth" {
					t.Error("unsealed claim invalid")
					return
				}
				stick.key, _ = airsimproto.DecodeBase64URL(reg["vowlan_secret"])
				response = map[string]string{"agent_id": "fixture", "route_proof": airsimproto.DirectRouteProof(stick.key, window.ID, peer, "fixture", 7592, 7593)}
			case "/v1/health":
				response = map[string]string{"agent_id": "fixture", "identity_proof": airsimproto.HealthProof(stick.key, nonce, "fixture"), "device_name": "Synthetic stick"}
			case "/api/status":
				response = map[string]string{"cellular_state": "registered", "operator": "fixture"}
			case "/api/sms":
				response = []map[string]string{{"delivery_id": "test-sms", "sender": "fixture", "content": "Synthetic", "timestamp": time.Now().UTC().Format(time.RFC3339Nano)}}
			case "/api/calls/dial":
				stick.current = true
				response = map[string]string{"call_id": "call-1", "operation_id": "attempt-01", "android_id": "call-1", "backend_id": "call-1", "operation_proof": airsimproto.DialProof(stick.key, nonce, "attempt-01", "call-1", "call-1")}
			case "/api/calls/status?observe=1&own=call-1", "/api/calls/status?observe=1&history=1":
				res := map[string]any{"active": nil, "history": []any{}}
				if stick.current {
					res["active"] = map[string]string{"id": "call-1", "state": "active", "direction": "outgoing"}
				}
				response = res
			case "/api/calls/hangup":
				stick.current = false
				response = map[string]bool{"ok": true}
			default:
				t.Errorf("unexpected request %s", path)
				return
			}
			raw, _ := json.Marshal(response)
			fmt.Fprintf(server, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(raw), raw)
		}()
		return client, nil
	}
	credential, err := enroll(ctx, dial, window.Code)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &backend{Backend: authority{}, line: "bt-line", client: newClient(dial, credential)}
	a := airsimgw.Identity{LineID: "bt-line", PairingID: "first"}
	b := airsimgw.Identity{LineID: "bt-line", PairingID: "second"}
	line, err := adapter.Line(ctx, a)
	if err != nil || !line.Registered || line.DeviceName != "Synthetic stick" {
		t.Fatalf("line=%+v err=%v", line, err)
	}
	messages, err := adapter.Messages(ctx, a, time.Time{}, 1)
	if err != nil || len(messages) != 1 || messages[0].DeliveryID != "test-sms" {
		t.Fatal("missing SMS", err)
	}
	call, err := adapter.Dial(ctx, a, "10010", "attempt-01")
	if err != nil || call.ID != "call-1" {
		t.Fatal("dial", err)
	}
	if _, err := adapter.AttachPCM(ctx, b, call.ID); !errors.Is(err, airsimgw.ErrForbidden) {
		t.Fatal("other participant gained media", err)
	}
	if err := adapter.CallAction(ctx, b, call.ID, "hangup", "", ""); !errors.Is(err, airsimgw.ErrForbidden) {
		t.Fatal("other participant gained control", err)
	}
	if err := adapter.KeepCall(ctx, b, call.ID); !errors.Is(err, airsimgw.ErrForbidden) {
		t.Fatal("other participant renewed lease", err)
	}
	pcm, err := adapter.AttachPCM(ctx, a, call.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer pcm.Close()
	frame, err := pcm.ReadDownlink(ctx)
	if err != nil || len(frame) != 320 || frame[0] != 0x55 {
		t.Fatal("speaker frame missing", err)
	}
	frame[0] = 0xaa
	if err := pcm.WriteUplink(ctx, frame); err != nil {
		t.Fatal(err)
	}
	if err := adapter.CallAction(ctx, a, call.ID, "hangup", "", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-pcm.Done():
	default:
		t.Fatal("stream remained open")
	}
}

func TestInterruptedMutationIsUnknownAndIsNotRetried(t *testing.T) {
	attempts := 0
	c := newClient(func(context.Context, byte) (net.Conn, error) {
		attempts++
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			reader := bufio.NewReader(server)
			_, _ = reader.ReadByte()
			request, err := http.ReadRequest(reader)
			if err == nil {
				_, _ = io.Copy(io.Discard, request.Body)
			}
			// The exact command may have already reached the modem.
		}()
		return client, nil
	}, Credential{Agent: "fixture", Key: [32]byte{1}})
	_, err := c.Dial(context.Background(), "10010", "attempt-01")
	if !errors.Is(err, ErrUnknown) || attempts != 1 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}
