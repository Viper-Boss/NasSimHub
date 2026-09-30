// Package nodeclient speaks the NasSimHub Node Protocol from the Core side.
//
// It holds the Core's long-lived signing key, performs pairing, maintains a
// bearer session and issues the ordinary calls. It reaches a Node through a
// transport.Selector, so every request automatically uses whichever link is up
// and a link change is invisible to callers.
package nodeclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/human-agent65535/nassimhub-node/core/transport"
	"github.com/human-agent65535/nassimhub-node/proto"
)

const maxResponseBytes = 4 << 20

// Identity is the Core's own identity, presented to every Node it owns.
type Identity struct {
	CoreID     string
	Name       string
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

// NewIdentity generates a fresh Core identity. Production Core persists this
// alongside its other secrets; regenerating it would orphan every pairing.
func NewIdentity(coreID, name string) (Identity, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, fmt.Errorf("generate core identity: %w", err)
	}
	return Identity{CoreID: coreID, Name: name, PublicKey: public, PrivateKey: private}, nil
}

// Client talks to one Node.
type Client struct {
	identity Identity
	selector *transport.Selector
	http     *http.Client
	now      func() time.Time

	mu      sync.Mutex
	session proto.Session
}

// Options configures a Client.
type Options struct {
	Identity Identity
	Selector *transport.Selector
	Timeout  time.Duration
	Now      func() time.Time
	// Transport carries the TLS trust decision. In production this is the
	// pinned transport nodetrust builds for exactly one device, which is what
	// makes "this connection reached the Node we adopted" a property of the
	// transport rather than something every call site has to remember to
	// check. Leaving it nil uses Go's default transport, which is correct only
	// for plaintext test servers.
	Transport http.RoundTripper
}

// New builds a Client.
func New(options Options) (*Client, error) {
	if options.Selector == nil {
		return nil, errors.New("a node client requires a transport selector")
	}
	if len(options.Identity.PrivateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("a node client requires a core signing key")
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Client{
		identity: options.Identity,
		selector: options.Selector,
		http:     &http.Client{Timeout: timeout, Transport: options.Transport},
		now:      now,
	}, nil
}

// DeviceID is the Node this client serves.
func (c *Client) DeviceID() string { return c.selector.DeviceID() }

// ConnectionType reports the link currently in use.
func (c *Client) ConnectionType() proto.ConnectionType { return c.selector.ConnectionType() }

// ---------------------------------------------------------------------------
// Discovery and pairing
// ---------------------------------------------------------------------------

// Describe reads the Node's unauthenticated identity document and verifies it.
//
// Verification matters at discovery time: the device_id is derived from the
// public key, so Core can confirm the Node is not claiming an identifier that
// belongs to another device before showing it to the user as "a new device".
func Describe(ctx context.Context, httpClient *http.Client, baseURL string) (proto.Node, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/node", nil)
	if err != nil {
		return proto.Node{}, err
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return proto.Node{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return proto.Node{}, fmt.Errorf("discovery returned %d", response.StatusCode)
	}
	var document proto.Node
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&document); err != nil {
		return proto.Node{}, fmt.Errorf("decode discovery document: %w", err)
	}
	key, err := proto.DecodePublicKey(document.PublicKey)
	if err != nil {
		return proto.Node{}, fmt.Errorf("node presented an unusable key: %w", err)
	}
	if !proto.ValidateDeviceID(document.DeviceID, document.Platform, key) {
		return proto.Node{}, fmt.Errorf("node claims device id %q which does not derive from its key", document.DeviceID)
	}
	return document, nil
}

// Pair adopts the Node.
func (c *Client) Pair(ctx context.Context) (proto.PairResponse, error) {
	body := proto.PairRequest{
		CoreID:        c.identity.CoreID,
		CoreName:      c.identity.Name,
		CorePublicKey: proto.EncodeKey(c.identity.PublicKey),
	}
	var response proto.PairResponse
	if err := c.signedCall(ctx, http.MethodPost, "/v1/pair", body, &response); err != nil {
		return proto.PairResponse{}, err
	}
	c.mu.Lock()
	c.session = response.Session
	c.mu.Unlock()
	return response, nil
}

// Unpair releases the Node.
func (c *Client) Unpair(ctx context.Context) (proto.UnpairResponse, error) {
	var response proto.UnpairResponse
	if err := c.call(ctx, http.MethodDelete, "/v1/pair", nil, &response); err != nil {
		return proto.UnpairResponse{}, err
	}
	c.mu.Lock()
	c.session = proto.Session{}
	c.mu.Unlock()
	return response, nil
}

// FactoryResetPairing clears the Node's trust relationship while keeping its
// identity, so the same physical device can be re-adopted without becoming a
// new row in the NAS.
func (c *Client) FactoryResetPairing(ctx context.Context) (proto.UnpairResponse, error) {
	var response proto.UnpairResponse
	body := proto.FactoryResetRequest{Confirm: c.DeviceID()}
	if err := c.call(ctx, http.MethodPost, "/v1/pair/factory-reset", body, &response); err != nil {
		return proto.UnpairResponse{}, err
	}
	c.mu.Lock()
	c.session = proto.Session{}
	c.mu.Unlock()
	return response, nil
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

func (c *Client) Node(ctx context.Context) (proto.Node, error) {
	endpoint, err := c.selector.Active(ctx)
	if err != nil {
		return proto.Node{}, err
	}
	return Describe(ctx, c.http, endpoint.BaseURL)
}

func (c *Client) Health(ctx context.Context) (proto.Health, error) {
	var health proto.Health
	endpoint, err := c.selector.Active(ctx)
	if err != nil {
		return proto.Health{}, err
	}
	err = c.raw(ctx, http.MethodGet, endpoint.BaseURL+"/v1/health", nil, nil, &health)
	return health, err
}

// Capabilities is the full capability document, including the voice detail.
type Capabilities struct {
	proto.Capabilities
	Voice *VoiceCapability `json:"voice,omitempty"`
	// Security is what THIS connection negotiated, as the Node saw it. It is
	// per-connection rather than per-device: the same Node answers this Core
	// over a post-quantum session and an older one classically, so it is only
	// meaningful alongside the request that produced it.
	Security proto.SecurityCapability `json:"security"`
}

// VoiceCapability mirrors the agent's detailed voice answer.
type VoiceCapability struct {
	Control bool           `json:"control"`
	Audio   bool           `json:"audio"`
	VoLTE   proto.Tristate `json:"volte"`
	Reason  string         `json:"reason,omitempty"`
	Source  string         `json:"source,omitempty"`
}

func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var capabilities Capabilities
	err := c.call(ctx, http.MethodGet, "/v1/capabilities", nil, &capabilities)
	return capabilities, err
}

func (c *Client) Version(ctx context.Context) (proto.VersionInfo, error) {
	var version proto.VersionInfo
	err := c.call(ctx, http.MethodGet, "/v1/version", nil, &version)
	return version, err
}

func (c *Client) Status(ctx context.Context) (proto.ModemStatus, error) {
	var status proto.ModemStatus
	err := c.call(ctx, http.MethodGet, "/v1/status", nil, &status)
	return status, err
}

func (c *Client) SIM(ctx context.Context) (proto.SIMInfo, error) {
	var info proto.SIMInfo
	err := c.call(ctx, http.MethodGet, "/v1/sim", nil, &info)
	return info, err
}

func (c *Client) Network(ctx context.Context) (proto.NetworkStatus, error) {
	var network proto.NetworkStatus
	err := c.call(ctx, http.MethodGet, "/v1/network", nil, &network)
	return network, err
}

func (c *Client) Signal(ctx context.Context) (proto.Signal, error) {
	var signal proto.Signal
	err := c.call(ctx, http.MethodGet, "/v1/signal", nil, &signal)
	return signal, err
}

func (c *Client) ListSMS(ctx context.Context) ([]proto.SMS, error) {
	var list proto.SMSList
	if err := c.call(ctx, http.MethodGet, "/v1/sms", nil, &list); err != nil {
		return nil, err
	}
	return list.Messages, nil
}

func (c *Client) SendSMS(ctx context.Context, request proto.SendSMSRequest) (proto.SendSMSResponse, error) {
	var response proto.SendSMSResponse
	err := c.call(ctx, http.MethodPost, "/v1/sms/send", request, &response)
	return response, err
}

func (c *Client) DeleteSMS(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodDelete, "/v1/sms/"+id, nil, nil)
}

func (c *Client) ListCalls(ctx context.Context) ([]proto.Call, error) {
	var list proto.CallList
	if err := c.call(ctx, http.MethodGet, "/v1/calls", nil, &list); err != nil {
		return nil, err
	}
	return list.Calls, nil
}

func (c *Client) Dial(ctx context.Context, request proto.DialRequest) (proto.CallReceipt, error) {
	var receipt proto.CallReceipt
	err := c.call(ctx, http.MethodPost, "/v1/calls/dial", request, &receipt)
	return receipt, err
}

func (c *Client) Answer(ctx context.Context, callID string) (proto.CallReceipt, error) {
	var receipt proto.CallReceipt
	err := c.call(ctx, http.MethodPost, "/v1/calls/"+callID+"/answer", nil, &receipt)
	return receipt, err
}

func (c *Client) Hangup(ctx context.Context, callID string) (proto.CallReceipt, error) {
	var receipt proto.CallReceipt
	err := c.call(ctx, http.MethodPost, "/v1/calls/"+callID+"/hangup", nil, &receipt)
	return receipt, err
}

func (c *Client) WiFi(ctx context.Context) (proto.WiFiStatus, error) {
	var status proto.WiFiStatus
	err := c.call(ctx, http.MethodGet, "/v1/wifi", nil, &status)
	return status, err
}

func (c *Client) WiFiScan(ctx context.Context) (proto.WiFiScanResult, error) {
	var result proto.WiFiScanResult
	err := c.call(ctx, http.MethodPost, "/v1/wifi/scan", nil, &result)
	return result, err
}

func (c *Client) WiFiConnect(ctx context.Context, request proto.WiFiConnectRequest) (proto.WiFiStatus, error) {
	var status proto.WiFiStatus
	err := c.call(ctx, http.MethodPost, "/v1/wifi/connect", request, &status)
	return status, err
}

func (c *Client) WiFiForget(ctx context.Context) (proto.WiFiStatus, error) {
	var status proto.WiFiStatus
	err := c.call(ctx, http.MethodPost, "/v1/wifi/forget", nil, &status)
	return status, err
}

// TransportKey fetches the Node's KCP transport secret over the authenticated
// TLS channel.
//
// This is the one response in the protocol that carries raw key material, so it
// is the one response a caller must not log, must not put in a diagnostics
// bundle, and must not store anywhere except the node store. The method is
// named for what it returns rather than for what it does, so that a reviewer
// grepping for where secrets flow finds it.
//
// rotate asks the Node to issue a NEW secret at the next epoch. The Node keeps
// accepting the previous one for the grace window in the response, so a
// rotation does not drop packets that were already in flight.
func (c *Client) TransportKey(ctx context.Context, rotate bool) (proto.TransportKeyResponse, error) {
	var response proto.TransportKeyResponse
	err := c.authenticatedCall(ctx, http.MethodPost, proto.TransportKeyPath,
		proto.TransportKeyRequest{Rotate: rotate}, &response)
	return response, err
}

func (c *Client) Logs(ctx context.Context, limit int, level proto.LogLevel) (proto.LogPage, error) {
	var page proto.LogPage
	path := fmt.Sprintf("/v1/logs?limit=%d", limit)
	if level != "" {
		path += "&level=" + string(level)
	}
	err := c.call(ctx, http.MethodGet, path, nil, &page)
	return page, err
}

// Diagnostics fetches the Node's sanitised diagnostics bundle.
//
// Core stores and forwards this without inspecting it: the redaction is the
// Node's job, done where the sensitive values actually live, and a second pass
// here would only create the impression that Core is a safety net it is not.
func (c *Client) Diagnostics(ctx context.Context) (proto.DiagnosticsBundle, error) {
	var bundle proto.DiagnosticsBundle
	err := c.call(ctx, http.MethodGet, "/v1/diagnostics", nil, &bundle)
	return bundle, err
}

// ---------------------------------------------------------------------------
// Request plumbing
// ---------------------------------------------------------------------------

// call issues a bearer-authenticated request, obtaining or renewing the session
// as needed. A 401 triggers exactly one re-authentication and retry, so an
// expired token is invisible to callers without risking a retry loop.
func (c *Client) call(ctx context.Context, method, path string, body, destination any) error {
	if err := c.ensureSession(ctx); err != nil {
		return err
	}
	err := c.authenticatedCall(ctx, method, path, body, destination)
	if err == nil {
		return nil
	}
	if proto.CodeOf(err) != proto.ErrorUnauthenticated {
		return err
	}
	c.mu.Lock()
	c.session = proto.Session{}
	c.mu.Unlock()
	if err := c.ensureSession(ctx); err != nil {
		return err
	}
	return c.authenticatedCall(ctx, method, path, body, destination)
}

func (c *Client) authenticatedCall(ctx context.Context, method, path string, body, destination any) error {
	endpoint, err := c.selector.Active(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	token := c.session.Token
	c.mu.Unlock()

	headers := map[string]string{"Authorization": "Bearer " + token}
	if err := c.raw(ctx, method, endpoint.BaseURL+path, body, headers, destination); err != nil {
		// A transport-level failure means this link is no longer usable, so the
		// next call re-selects instead of retrying a dead address.
		var networkError *url
		if errors.As(err, &networkError) {
			c.selector.Invalidate()
		}
		return err
	}
	return nil
}

func (c *Client) ensureSession(ctx context.Context) error {
	c.mu.Lock()
	// Renew a little before expiry so a request cannot land in the gap.
	valid := c.session.Token != "" && c.now().Add(30*time.Second).Before(c.session.ExpiresAt)
	c.mu.Unlock()
	if valid {
		return nil
	}
	var session proto.Session
	if err := c.signedCall(ctx, http.MethodPost, "/v1/session", proto.SessionRequest{CoreID: c.identity.CoreID}, &session); err != nil {
		return err
	}
	c.mu.Lock()
	c.session = session
	c.mu.Unlock()
	return nil
}

// signedCall issues a request signed with the Core's long-lived key.
func (c *Client) signedCall(ctx context.Context, method, path string, body, destination any) error {
	endpoint, err := c.selector.Active(ctx)
	if err != nil {
		return err
	}
	encoded, err := encode(body)
	if err != nil {
		return err
	}
	nonce, err := newNonce()
	if err != nil {
		return err
	}
	stamp := proto.FormatTimestamp(c.now())
	signature := ed25519.Sign(c.identity.PrivateKey, proto.SigningString(method, path, stamp, nonce, encoded))

	headers := map[string]string{
		proto.HeaderCoreID:    c.identity.CoreID,
		proto.HeaderTimestamp: stamp,
		proto.HeaderNonce:     nonce,
		proto.HeaderSignature: base64.StdEncoding.EncodeToString(signature),
	}
	return c.raw(ctx, method, endpoint.BaseURL+path, body, headers, destination)
}

// url wraps a transport-level failure so call can tell it apart from a coded
// protocol error and invalidate the link.
type url struct{ err error }

func (u *url) Error() string { return u.err.Error() }
func (u *url) Unwrap() error { return u.err }

func (c *Client) raw(ctx context.Context, method, target string, body any, headers map[string]string, destination any) error {
	encoded, err := encode(body)
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return &url{err: err}
	}
	defer response.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return &url{err: err}
	}
	if response.StatusCode >= 400 {
		var failure proto.ErrorBody
		if json.Unmarshal(payload, &failure) == nil && failure.Error.Code != "" {
			return &proto.OperationError{
				Code:      failure.Error.Code,
				Operation: failure.Error.Operation,
				Message:   failure.Error.Message,
			}
		}
		return &proto.OperationError{
			Code:    codeForStatus(response.StatusCode),
			Message: fmt.Sprintf("node returned %d", response.StatusCode),
		}
	}
	if destination == nil || len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, destination); err != nil {
		return fmt.Errorf("decode node response: %w", err)
	}
	return nil
}

// codeForStatus recovers a code when a proxy has replaced the body.
func codeForStatus(status int) proto.ErrorCode {
	switch status {
	case http.StatusBadRequest:
		return proto.ErrorInvalidArgument
	case http.StatusUnauthorized:
		return proto.ErrorUnauthenticated
	case http.StatusForbidden:
		return proto.ErrorPermissionDenied
	case http.StatusNotFound:
		return proto.ErrorNotFound
	case http.StatusConflict:
		return proto.ErrorConflict
	case http.StatusPreconditionFailed:
		return proto.ErrorFailedPrecondition
	case http.StatusNotImplemented:
		return proto.ErrorNotSupported
	case http.StatusServiceUnavailable:
		return proto.ErrorUnavailable
	default:
		return proto.ErrorInternal
	}
}

func encode(body any) ([]byte, error) {
	if body == nil {
		return []byte{}, nil
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	return encoded, nil
}

func newNonce() (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
