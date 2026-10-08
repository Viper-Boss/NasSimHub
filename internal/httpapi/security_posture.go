package httpapi

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/auth"
	"github.com/human-agent65535/modemdeck/internal/release"
)

// The headers the web container's nginx sets on every request it proxies to
// the API, describing the connection the VIEWER is on. The API never
// terminates TLS itself (it listens on plain HTTP inside the compose network),
// so this is the only way it can know what protected the request.
//
// nginx overwrites each of these, so a browser cannot set them: a header named
// in proxy_set_header is never passed through from the client, even when the
// value nginx sets is empty and the header is therefore dropped. Someone who
// reaches the API port directly, bypassing nginx, can set them -- and all it
// buys them is a wrong picture of their own connection, since this endpoint
// describes nothing but the connection it is asked over.
const (
	edgeHeader        = "X-ModemDeck-Edge"
	edgeTLSProtocol   = "X-ModemDeck-TLS-Protocol"
	edgeTLSGroup      = "X-ModemDeck-TLS-Group"
	edgeTLSCipher     = "X-ModemDeck-TLS-Cipher"
	edgeDirect        = "direct"
	edgeCloudflare    = "cloudflare"
	maxEdgeHeaderSize = 64
)

// certificateExpiryWarning is when the key state turns amber. It matches the
// automatic certificate's renewal window, so an automatic certificate that is
// inside it is one whose renewal has not happened yet.
const certificateExpiryWarning = 30 * 24 * time.Hour

type postureRow struct {
	State  string `json:"state"`
	Value  string `json:"value,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type edgeConnection struct {
	edge     string
	protocol string
	group    string
	cipher   string
}

func readEdgeConnection(header http.Header) edgeConnection {
	read := func(name string) string {
		value := strings.TrimSpace(header.Get(name))
		if len(value) > maxEdgeHeaderSize {
			return ""
		}
		return value
	}
	return edgeConnection{
		edge:     strings.ToLower(read(edgeHeader)),
		protocol: read(edgeTLSProtocol),
		group:    read(edgeTLSGroup),
		cipher:   read(edgeTLSCipher),
	}
}

// securityPosture answers GET /api/v1/security/posture, the dashboard's
// "secure transport" card.
//
// EVERY ROW IS A FACT ABOUT SOMETHING THE GATEWAY CAN SEE, and a row it
// cannot back is either said plainly ("off", "unknown") or left out. The card
// was drawn first, against an aspirational design -- public-key pinning, HMAC
// packet auth, dual-signed OTA -- and most of that has nothing behind it in
// this product. Filling those rows in to match the drawing would be the card
// inventing its own subject.
//
// What each row is, concretely:
//
//	transport     the TLS version of THIS request, as nginx negotiated it
//	post_quantum  whether that handshake used a hybrid ML-KEM group
//	packet_auth   the AEAD (or MAC) protecting that connection's records
//	replay        TLS's own record sequencing, with 0-RTT not enabled
//	identity      the key of the certificate the gateway is serving
//	pinning       always "off": nothing in the product pins a key
//	ota           the release-manifest signature, always required
//
// Through Cloudflare the browser's TLS ends at Cloudflare, so the rows about
// that handshake answer "unknown" rather than describing the tunnel's
// internal hop as if it were the viewer's.
func (api *API) securityPosture(response http.ResponseWriter, request *http.Request) {
	connection := readEdgeConnection(request.Header)
	body := map[string]any{
		"pinning": postureRow{State: "off"},
		"ota":     otaRow(),
	}
	switch connection.edge {
	case edgeDirect:
		body["transport"] = transportRow(connection.protocol)
		if connection.protocol != "" {
			body["post_quantum"] = postQuantumRow(connection.group)
			if value := recordProtection(connection.cipher); value != "" {
				body["packet_auth"] = postureRow{State: "ok", Value: value, Detail: connection.cipher}
			}
			body["replay"] = postureRow{State: "ok"}
		}
		if row, known := api.identityRow(); known {
			body["identity"] = row
		}
	case edgeCloudflare:
		// nginx sends visitors arriving as plain HTTP back to HTTPS before
		// they reach the API, so a request here came over TLS to Cloudflare.
		body["transport"] = postureRow{State: "ok", Value: "Cloudflare"}
		unknown := postureRow{State: "unknown"}
		body["post_quantum"] = unknown
		body["packet_auth"] = unknown
		body["replay"] = unknown
		body["identity"] = postureRow{State: "ok", Value: "Cloudflare"}
	}
	// Each link a request crosses, reported separately; see
	// security_boundaries.go. Additive: older pages ignore it.
	body["boundaries"] = api.linkBoundaries(request, connection)
	if connection.edge == edgeDirect && viaTailscaleServe(request.Header) {
		// The rows above describe what nginx negotiated - which, through
		// Tailscale Serve, is the local hop from Tailscale to the gateway and
		// not the browser's own handshake. Say whose handshake it is.
		body["transport_scope"] = "proxy_hop"
		body["proxy"] = postureRow{State: "ok", Value: "Tailscale Serve"}
	}
	if events := api.securityEvents(request, connection); len(events) > 0 {
		body["events"] = events
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, body)
}

func transportRow(protocol string) postureRow {
	switch protocol {
	case "TLSv1.3":
		return postureRow{State: "ok", Value: "TLS 1.3"}
	case "TLSv1.2":
		return postureRow{State: "ok", Value: "TLS 1.2"}
	case "":
		return postureRow{State: "warn", Value: "HTTP"}
	}
	return postureRow{State: "warn", Value: protocol}
}

// The key-exchange groups nginx can name, in both the forms it uses: OpenSSL's
// short name for a group it has a NID for, and "0x" plus the TLS code point for
// one it has not -- which is how the hybrid ML-KEM groups come out of OpenSSL
// 3.5.
var (
	hybridGroups = map[string]string{
		"x25519mlkem768":        "X25519MLKEM768",
		"0x11ec":                "X25519MLKEM768",
		"secp256r1mlkem768":     "SecP256r1MLKEM768",
		"0x11eb":                "SecP256r1MLKEM768",
		"secp384r1mlkem1024":    "SecP384r1MLKEM1024",
		"0x11ed":                "SecP384r1MLKEM1024",
		"x25519kyber768draft00": "X25519Kyber768Draft00",
		"0x6399":                "X25519Kyber768Draft00",
		"mlkem512":              "MLKEM512",
		"0x0200":                "MLKEM512",
		"mlkem768":              "MLKEM768",
		"0x0201":                "MLKEM768",
		"mlkem1024":             "MLKEM1024",
		"0x0202":                "MLKEM1024",
	}
	classicalGroups = map[string]string{
		"x25519":     "X25519",
		"0x001d":     "X25519",
		"x448":       "X448",
		"0x001e":     "X448",
		"prime256v1": "P-256",
		"secp256r1":  "P-256",
		"0x0017":     "P-256",
		"secp384r1":  "P-384",
		"0x0018":     "P-384",
		"secp521r1":  "P-521",
		"0x0019":     "P-521",
	}
)

// postQuantumRow reports whether this handshake used a post-quantum group. A
// group it does not recognise -- or none, which is what nginx gives when it
// cannot name one -- is "unknown", with the raw name as the detail: guessing
// either way about it would be a claim.
func postQuantumRow(group string) postureRow {
	key := strings.ToLower(group)
	if name, ok := hybridGroups[key]; ok {
		return postureRow{State: "ok", Value: "ML-KEM", Detail: name}
	}
	if name, ok := classicalGroups[key]; ok {
		return postureRow{State: "off", Detail: name}
	}
	return postureRow{State: "unknown", Detail: group}
}

// recordProtection names what authenticates every record on the connection,
// from nginx's cipher name in either its TLS 1.3 (TLS_AES_128_GCM_SHA256) or
// OpenSSL TLS 1.2 (ECDHE-ECDSA-AES128-GCM-SHA256) spelling.
func recordProtection(cipher string) string {
	name := strings.ToUpper(cipher)
	switch {
	case name == "":
		return ""
	case strings.Contains(name, "CHACHA20") && strings.Contains(name, "POLY1305"):
		return "ChaCha20-Poly1305"
	case strings.Contains(name, "AES_128_GCM") || strings.Contains(name, "AES128-GCM"):
		return "AES-128-GCM"
	case strings.Contains(name, "AES_256_GCM") || strings.Contains(name, "AES256-GCM"):
		return "AES-256-GCM"
	case strings.Contains(name, "CCM"):
		return "AES-CCM"
	case strings.HasSuffix(name, "-SHA384"):
		return "HMAC-SHA384"
	case strings.HasSuffix(name, "-SHA256"):
		return "HMAC-SHA256"
	case strings.HasSuffix(name, "-SHA"):
		return "HMAC-SHA1"
	}
	return ""
}

// identityRow is the certificate the gateway presents on its own HTTPS port.
func (api *API) identityRow() (postureRow, bool) {
	if api.tlsSettingsService == nil {
		return postureRow{}, false
	}
	status := api.tlsSettingsService.Status()
	if status.KeyAlgorithm == "" {
		return postureRow{}, false
	}
	state := "ok"
	if status.Expired {
		state = "warn"
	}
	return postureRow{State: state, Value: status.KeyAlgorithm, Detail: status.FingerprintSHA256}, true
}

// otaRow is the release-manifest signature. It is compiled in and cannot be
// switched off -- the unsigned loader was removed -- so it is always on. One
// Ed25519 key, the same one that signs licences; the detail is its fingerprint
// in the form the update card and the issuing console print it.
func otaRow() postureRow {
	row := postureRow{State: "ok", Value: "Ed25519"}
	if release.PQSignaturePolicy() == release.PolicyDualRequired {
		row.Value = "Ed25519 + " + release.PQSigningAlgorithm
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(release.SigningPublicKey))
	if err == nil && len(decoded) == ed25519.PublicKeySize {
		row.Detail = hex.EncodeToString(decoded[:4])
	}
	return row
}

// securityEvents is the card's lower panel. Gateway-wide facts (failed
// logins, the certificate's life) go to administrators only; a household
// member is told about their own connection and nothing about anyone else's
// attempts.
//
// `replay_blocked` is never sent: nothing counts rejected replays, and the
// card shows a dash for it rather than a zero.
func (api *API) securityEvents(request *http.Request, connection edgeConnection) map[string]any {
	events := map[string]any{}
	if connection.edge == edgeDirect {
		// HSTS (nginx sends it on every HTTPS response) stops a fall back to
		// plain HTTP, and TLS 1.3's downgrade sentinel stops a fall back to an
		// older TLS. Both are in force exactly when the request came over TLS.
		events["downgrade_guard"] = connection.protocol != ""
	}
	if !requestIsAdmin(request) {
		return events
	}
	if api.loginFailures != nil {
		failures, watched := api.loginFailures.history.recent(time.Now())
		// Whole hours actually watched. Under an hour after a restart there is
		// no honest window to put on the figure, so it is not sent.
		if hours := int(watched / time.Hour); hours > 0 {
			events["window_hours"] = hours
			events["anomalous_logins"] = failures
		}
	}
	if state := api.certificateKeyState(time.Now()); state != "" {
		events["key_state"] = state
	}
	return events
}

func (api *API) certificateKeyState(now time.Time) string {
	if api.tlsSettingsService == nil {
		return ""
	}
	status := api.tlsSettingsService.Status()
	if status.Expired {
		return "expired"
	}
	notAfter, err := time.Parse(time.RFC3339, status.NotAfter)
	if err != nil {
		return ""
	}
	if notAfter.Sub(now) <= certificateExpiryWarning {
		return "warn"
	}
	return "ok"
}

func requestIsAdmin(request *http.Request) bool {
	principal, ok := auth.PrincipalFromContext(request.Context())
	// No principal only happens with authentication disabled (tests), which
	// requireAdmin treats the same way.
	return !ok || principal.IsAdmin()
}
