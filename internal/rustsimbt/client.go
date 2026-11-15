// SPDX-License-Identifier: AGPL-3.0-only
// Package rustsimbt carries signed RustSimHub gateway requests and a separate
// call PCM stream. It exposes no network dial or proxy operation to consumers.
package rustsimbt

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/internal/airsimproto"
)

const (
	controlChannel byte = 23
	audioChannel   byte = 24
	requestLimit        = 96 << 10
)

var (
	ErrInvalid = errors.New("RustSimHub Bluetooth: invalid command or reply")
	ErrDenied  = errors.New("RustSimHub Bluetooth: authentication or authority denied")
	ErrUnknown = errors.New("RustSimHub Bluetooth: command outcome unknown")
)

type dialFunc func(context.Context, byte) (net.Conn, error)

type Credential struct {
	Agent string   `json:"agent_id"`
	Key   [32]byte `json:"key"`
}

type Client struct {
	dial       dialFunc
	credential Credential
	control    chan struct{}
	mu         sync.Mutex
	owned      string
	audio      *PCM
}

func newClient(dial dialFunc, credential Credential) *Client {
	return &Client{dial: dial, credential: credential, control: make(chan struct{}, 1)}
}

func random(n int) ([]byte, error) {
	out := make([]byte, n)
	_, err := rand.Read(out)
	return out, err
}

func (c *Client) exchange(ctx context.Context, pairing bool, method, path string, input, output any) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case c.control <- struct{}{}:
		defer func() { <-c.control }()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil || len(body) > requestLimit {
			return "", ErrInvalid
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://rustsimhub.invalid"+path, bytes.NewReader(body))
	if err != nil {
		return "", ErrInvalid
	}
	request.Close = true
	var nonce string
	selector := byte(2)
	if !pairing {
		raw, err := random(18)
		if err != nil {
			return "", err
		}
		selector = 1
		nonce = airsimproto.Base64URL(raw)
		now := time.Now().Unix()
		request.Header.Set(airsimproto.HeaderVersion, "1")
		request.Header.Set(airsimproto.HeaderTimestamp, fmt.Sprint(now))
		request.Header.Set(airsimproto.HeaderNonce, nonce)
		request.Header.Set(airsimproto.HeaderSignature, airsimproto.Sign(c.credential.Key[:], airsimproto.Canonical(method, path, body, now, nonce)))
	}
	conn, err := c.dial(ctx, controlChannel)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return "", err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if _, err := conn.Write([]byte{selector}); err != nil {
		return "", err
	}
	if err := request.Write(conn); err != nil {
		return "", unknownWrite(method, err)
	}
	reply, err := http.ReadResponse(bufio.NewReader(io.LimitReader(conn, requestLimit+8192)), request)
	if err != nil {
		return "", unknownWrite(method, err)
	}
	defer reply.Body.Close()
	if reply.StatusCode == 401 || reply.StatusCode == 403 {
		return "", ErrDenied
	}
	if reply.StatusCode < 200 || reply.StatusCode > 299 {
		return "", unknownWrite(method, fmt.Errorf("RustSimHub Bluetooth: status %d", reply.StatusCode))
	}
	if len(reply.TransferEncoding) != 0 || reply.ContentLength < 0 || reply.ContentLength > requestLimit {
		return "", unknownWrite(method, ErrInvalid)
	}
	raw, err := io.ReadAll(io.LimitReader(reply.Body, requestLimit+1))
	if err != nil || len(raw) > requestLimit || json.Unmarshal(raw, output) != nil {
		return "", unknownWrite(method, ErrInvalid)
	}
	return nonce, nil
}

func unknownWrite(method string, err error) error {
	if method == http.MethodPost {
		return errors.Join(ErrUnknown, err)
	}
	return err
}

func enroll(ctx context.Context, dial dialFunc, code string) (Credential, error) {
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		return Credential{}, ErrInvalid
	}
	c := newClient(dial, Credential{})
	var info struct {
		Session string `json:"session_id"`
		Public  string `json:"server_public_key"`
	}
	if _, err := c.exchange(ctx, true, "GET", "/pair/v1/info", nil, &info); err != nil {
		return Credential{}, err
	}
	server, err := airsimproto.DecodeBase64URL(info.Public)
	if err != nil || len(server) != 32 || len(info.Session) == 0 || len(info.Session) > 128 {
		return Credential{}, ErrInvalid
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return Credential{}, err
	}
	var credential Credential
	if _, err := rand.Read(credential.Key[:]); err != nil {
		return Credential{}, err
	}
	registration, err := json.Marshal(map[string]string{
		"vowlan_secret": airsimproto.Base64URL(credential.Key[:]),
		"device_id":     "NasSimHub-Bluetooth",
	})
	if err != nil {
		return Credential{}, err
	}
	nonce, err := random(12)
	if err != nil {
		return Credential{}, err
	}
	sealed, err := airsimproto.SealRegistration(private, server, registration, info.Session, code, nonce)
	if err != nil {
		return Credential{}, err
	}
	clientKey := airsimproto.Base64URL(private.PublicKey().Bytes())
	var reply struct {
		Agent string `json:"agent_id"`
		Proof string `json:"route_proof"`
	}
	_, err = c.exchange(ctx, true, "POST", "/pair/v1/claim", map[string]any{
		"version": 1, "session_id": info.Session, "client_public_key": clientKey,
		"sealed_registration": airsimproto.Base64URL(sealed),
	}, &reply)
	if err != nil {
		return Credential{}, err
	}
	expected := airsimproto.DirectRouteProof(credential.Key[:], info.Session, clientKey, reply.Agent, 7592, 7593)
	if reply.Agent == "" || len(reply.Agent) > 160 || !hmac.Equal([]byte(reply.Proof), []byte(expected)) {
		return Credential{}, errors.Join(ErrUnknown, ErrDenied)
	}
	credential.Agent = reply.Agent
	return credential, nil
}

type Device struct {
	Name         string `json:"device_name"`
	Model        string `json:"model"`
	Manufacturer string `json:"manufacturer"`
	Number       string `json:"phone_number"`
	Agent        string `json:"agent_id"`
	Proof        string `json:"identity_proof"`
}

func (c *Client) Device(ctx context.Context) (Device, error) {
	var device Device
	nonce, err := c.exchange(ctx, false, "GET", "/v1/health", nil, &device)
	if err != nil {
		return Device{}, err
	}
	expected := airsimproto.HealthProof(c.credential.Key[:], nonce, device.Agent)
	if device.Agent != c.credential.Agent || !hmac.Equal([]byte(device.Proof), []byte(expected)) {
		return Device{}, ErrDenied
	}
	device.Proof = ""
	return device, nil
}

type LineStatus struct {
	CellularState string `json:"cellular_state"`
	Operator      string `json:"operator"`
	Firmware      string `json:"firmware"`
	SignalDBM     *int   `json:"signal_dbm"`
}

func (c *Client) Status(ctx context.Context) (LineStatus, error) {
	var status LineStatus
	_, err := c.exchange(ctx, false, "GET", "/api/status", nil, &status)
	return status, err
}

type Call struct {
	ID        string `json:"id"`
	Direction string `json:"direction"`
	State     string `json:"state"`
	Number    string `json:"number"`
	Started   string `json:"started_at"`
	Updated   string `json:"updated_at"`
	Ended     string `json:"ended_at"`
	Missed    bool   `json:"missed"`
}

func (c *Client) Calls(ctx context.Context) (active *Call, history []Call, err error) {
	var response struct {
		Active  *Call  `json:"active"`
		History []Call `json:"history"`
	}
	_, err = c.exchange(ctx, false, "GET", "/api/calls/status?observe=1&history=1", nil, &response)
	return response.Active, response.History, err
}

type Message struct {
	ID        string `json:"delivery_id"`
	Sender    string `json:"sender"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}

func (c *Client) Messages(ctx context.Context) ([]Message, error) {
	var messages []Message
	_, err := c.exchange(ctx, false, "GET", "/api/sms", nil, &messages)
	return messages, err
}

func (c *Client) Dial(ctx context.Context, number, operation string) (Call, error) {
	if len(number) < 3 || len(number) > 64 || len(operation) < 8 || len(operation) > 64 {
		return Call{}, ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.owned != "" {
		return Call{}, ErrDenied
	}
	var response struct {
		ID        string `json:"call_id"`
		Operation string `json:"operation_id"`
		Android   string `json:"android_id"`
		Backend   string `json:"backend_id"`
		Proof     string `json:"operation_proof"`
	}
	nonce, err := c.exchange(ctx, false, "POST", "/api/calls/dial", map[string]string{
		"number": number, "operation_id": operation,
	}, &response)
	if err != nil {
		return Call{}, err
	}
	expected := airsimproto.DialProof(c.credential.Key[:], nonce, operation, response.Android, response.Backend)
	if response.ID == "" || len(response.ID) > 160 || response.Operation != operation ||
		response.ID != response.Android || response.ID != response.Backend ||
		!hmac.Equal([]byte(response.Proof), []byte(expected)) {
		return Call{}, errors.Join(ErrUnknown, ErrDenied)
	}
	c.owned = response.ID
	return Call{ID: response.ID, Direction: "outgoing", State: "dialing", Number: number}, nil
}

func (c *Client) Action(ctx context.Context, id, action, digit string) error {
	if id == "" || len(id) > 160 {
		return ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch action {
	case "answer", "reject":
		if c.owned != "" {
			return ErrDenied
		}
	case "hangup", "dtmf":
		if c.owned != id {
			return ErrDenied
		}
	default:
		return ErrInvalid
	}
	if action == "dtmf" && (len(digit) != 1 || !strings.Contains("0123456789*#", digit)) {
		return ErrInvalid
	}
	var reply struct{}
	_, err := c.exchange(ctx, false, "POST", "/api/calls/"+action, map[string]string{"call_id": id, "digit": digit}, &reply)
	if err != nil {
		return err
	}
	if action == "answer" {
		c.owned = id
	} else if action == "hangup" {
		c.owned = ""
		if c.audio != nil {
			c.audio.Close()
			c.audio = nil
		}
	}
	return nil
}

// Keep checks and renews only the exact call owned by this client.
func (c *Client) Keep(ctx context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.keep(ctx, id)
}

func (c *Client) keep(ctx context.Context, id string) error {
	if id == "" || c.owned != id {
		return ErrDenied
	}
	var reply struct {
		Active *Call `json:"active"`
	}
	_, err := c.exchange(ctx, false, "GET", "/api/calls/status?observe=1&own="+url.QueryEscape(id), nil, &reply)
	if err != nil {
		return err
	}
	if reply.Active == nil || reply.Active.ID != id {
		c.owned = ""
		if c.audio != nil {
			c.audio.Close()
			c.audio = nil
		}
		return ErrDenied
	}
	return nil
}

func (c *Client) OpenPCM(ctx context.Context, id string) (*PCM, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.keep(ctx, id); err != nil {
		return nil, err
	}
	if c.audio != nil {
		select {
		case <-c.audio.Done():
		default:
			return nil, ErrDenied
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	conn, err := c.dial(ctx, audioChannel)
	if err != nil {
		return nil, err
	}
	pcm, err := c.openPCM(ctx, conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	c.audio = pcm
	return pcm, nil
}

func (c *Client) openPCM(ctx context.Context, conn net.Conn) (*PCM, error) {
	raw, err := random(18)
	if err != nil {
		return nil, err
	}
	preface := airsimproto.PCMPreface{Timestamp: time.Now().Unix(), Nonce: airsimproto.Base64URL(raw)}
	signature := airsimproto.Sign(c.credential.Key[:], airsimproto.PCMCanonical(preface))
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(conn, "%s %d %s %s\n", airsimproto.PCMMagic, preface.Timestamp, preface.Nonce, signature); err != nil {
		return nil, err
	}
	var rate uint32
	if err := binary.Read(conn, binary.BigEndian, &rate); err != nil {
		return nil, err
	}
	if rate != 8000 && rate != 16000 {
		return nil, ErrInvalid
	}
	marker := make([]byte, len(airsimproto.PCMReady))
	if _, err := io.ReadFull(conn, marker); err != nil {
		return nil, err
	}
	if string(marker) != airsimproto.PCMReady {
		return nil, ErrDenied
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	return &PCM{conn: conn, rate: int(rate), done: make(chan struct{})}, nil
}

type PCM struct {
	conn  net.Conn
	rate  int
	done  chan struct{}
	close sync.Once
}

func (p *PCM) SampleRate() int       { return p.rate }
func (p *PCM) FrameBytes() int       { return p.rate / 25 }
func (p *PCM) Done() <-chan struct{} { return p.done }
func (p *PCM) Close()                { p.close.Do(func() { close(p.done); p.conn.Close() }) }

func (p *PCM) ReadDownlink(ctx context.Context) ([]byte, error) {
	frame := make([]byte, p.FrameBytes())
	deadline := time.Now().Add(2 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := p.conn.SetReadDeadline(deadline); err != nil {
		p.Close()
		return nil, err
	}
	stop := context.AfterFunc(ctx, p.Close)
	defer stop()
	if _, err := io.ReadFull(p.conn, frame); err != nil {
		p.Close()
		return nil, err
	}
	return frame, nil
}

func (p *PCM) WriteUplink(ctx context.Context, frame []byte) error {
	if len(frame) != p.FrameBytes() {
		return ErrInvalid
	}
	deadline := time.Now().Add(60 * time.Millisecond)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := p.conn.SetWriteDeadline(deadline); err != nil {
		p.Close()
		return err
	}
	stop := context.AfterFunc(ctx, p.Close)
	defer stop()
	if len(frame) == 0 {
		return ErrInvalid
	}
	n, err := p.conn.Write(frame)
	if err == nil && n != len(frame) {
		err = io.ErrShortWrite
	}
	if err != nil {
		p.Close()
	}
	return err
}

func (c *Client) release(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.owned == id {
		c.owned = ""
		if c.audio != nil {
			c.audio.Close()
			c.audio = nil
		}
	}
}
