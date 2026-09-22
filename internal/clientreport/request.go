// Package clientreport authenticates a console's request for a gateway's
// client report.
//
// # WHY A SIGNATURE IS REQUIRED HERE AND NOT FOR THE IDENTITY REQUEST
//
// The gateway already answers an unauthenticated identity request over NKN with
// its machine ID and its module IMEIs. That is a deliberate, documented trade:
// both are semi-public (the machine ID is a storage fingerprint hash, the IMEI
// is printed on the module), and knowing them lets somebody ASK for a
// certificate, not make one.
//
// The client report is a different category and must not ride on that trade:
//
//	Software and module versions tell a scanner exactly which known holes
//	apply to which gateway. Enumerating NKN addresses would produce a
//	target list.
//
//	The entitlement list says what this customer paid for.
//
//	The anti-fraud counters are behavioural: how close this installation is
//	to its daily limit, and whether the limit was lifted.
//
// So the report is released only to a party that proves possession of the
// issuer's private key -- the same key that signs certificates, which is the
// only party with any business asking. Everything else gets silence.
package clientreport

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Domain is the first line of the signed request.
//
// The issuer key now signs three different kinds of thing: licence
// certificates, release manifests, and these requests. Three uses of one key is
// three chances for a cross-protocol replay, where a signature obtained
// legitimately for one purpose is presented for another. All three encodings
// are made mutually exclusive by their first line:
//
//	licence certificate   v=<integer>
//	release manifest      nsh-domain=release-manifest-v1
//	client report request nsh-domain=client-report-request-v1
//
// TestTheThreeSignatureDomainsAreMutuallyExclusive asserts every pairing.
const Domain = "nsh-domain=client-report-request-v1"

// FreshnessWindow bounds how far a request's timestamp may be from the
// gateway's clock, in either direction.
//
// This is the replay bound, and it is only half the story: see Verify, where
// the request is also pinned to one machine. Five minutes is chosen to survive
// ordinary clock drift on a NAS with no NTP -- narrower would reject honest
// requests, and a gateway that refuses its own issuer is worse here than a
// five-minute replay window on a read-only query.
const FreshnessWindow = 5 * time.Minute

const (
	maxNonceLength     = 64
	minNonceLength     = 8
	maxMachineIDLength = 128
)

var (
	// ErrBadSignature means the request was not signed by the issuer. It is
	// never a reason to answer anyway.
	ErrBadSignature = errors.New("clientreport: signature does not verify")
	// ErrMalformed covers anything structurally wrong.
	ErrMalformed = errors.New("clientreport: request is malformed")
	// ErrStale means the timestamp is outside the freshness window.
	ErrStale = errors.New("clientreport: request is outside the freshness window")
	// ErrWrongMachine means the request was signed for a different gateway.
	ErrWrongMachine = errors.New("clientreport: request names another machine")
)

// Request is what the issuing console sends.
type Request struct {
	Type    string `json:"type"`
	Version int    `json:"version"`
	// Nonce is echoed in the reply so the console can match answer to question.
	Nonce string `json:"nonce"`
	// MachineID is the gateway this request is FOR, and it is signed.
	//
	// This is what stops a captured request being replayed anywhere else: a
	// gateway answers only when the signed machine ID is its own, so one
	// intercepted request is worth one gateway for five minutes rather than
	// every gateway forever. It also means the console must learn the machine
	// ID first, via the unauthenticated identity request -- which is the step
	// that was already there.
	MachineID string `json:"machine_id"`
	IssuedAt  int64  `json:"issued_at"`
	Signature string `json:"signature"`
}

// canonical renders the exact bytes that get signed.
//
// Line-oriented rather than JSON, for the same reason as the certificate and
// the manifest: the signature has to survive a Go version bump, a field
// reordering, and being reimplemented in the console.
func (request Request) canonical() ([]byte, error) {
	if err := request.validate(); err != nil {
		return nil, err
	}
	var builder strings.Builder
	builder.WriteString(Domain)
	builder.WriteByte('\n')
	write := func(key, value string) {
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(value)
		builder.WriteByte('\n')
	}
	write("nonce", request.Nonce)
	write("machine_id", request.MachineID)
	write("issued_at", strconv.FormatInt(request.IssuedAt, 10))
	return []byte(builder.String()), nil
}

// Canonical exposes the signed bytes so the signing tool signs what this
// verifies, and so a test can pin them.
func (request Request) Canonical() ([]byte, error) {
	return request.canonical()
}

// validate refuses anything that would make the canonical form ambiguous.
//
// The separator is "\n", so a value containing one could be split differently
// by a reader than by the writer -- two different requests with one signature
// between them. Refusing the character outright beats escaping it.
func (request Request) validate() error {
	if request.Type != Type {
		return fmt.Errorf("%w: type %q", ErrMalformed, request.Type)
	}
	if request.Version != Version {
		return fmt.Errorf("%w: version %d", ErrMalformed, request.Version)
	}
	if len(request.Nonce) < minNonceLength || len(request.Nonce) > maxNonceLength {
		return fmt.Errorf("%w: nonce length %d", ErrMalformed, len(request.Nonce))
	}
	if request.MachineID == "" || len(request.MachineID) > maxMachineIDLength {
		return fmt.Errorf("%w: machine id", ErrMalformed)
	}
	if request.IssuedAt <= 0 {
		return fmt.Errorf("%w: issued_at", ErrMalformed)
	}
	for _, value := range []string{request.Nonce, request.MachineID} {
		if strings.ContainsAny(value, "\n=") {
			return fmt.Errorf("%w: a field contains a separator", ErrMalformed)
		}
	}
	return nil
}

const (
	// Type and Version identify this protocol on the wire.
	Type    = "homesim.client.report"
	Version = 1
)

// Verify decides whether this gateway should answer.
//
// Fail-closed at every step, and in this order on purpose: structure, then
// machine binding, then freshness, then cryptography. The cheap checks discard
// a flood of junk before any signature verification, which is the only
// expensive part -- an unauthenticated NKN endpoint that did the Ed25519
// verification first would be a free way to spend a gateway's CPU.
//
// machineID is this gateway's own. An empty one means the gateway cannot
// establish its own identity, so it cannot confirm the request was meant for
// it, so it refuses: the binding is load-bearing and an unverifiable binding is
// no binding.
func Verify(
	request Request,
	machineID string,
	publicKeyBase64 string,
	now time.Time,
) error {
	if err := request.validate(); err != nil {
		return err
	}
	if strings.TrimSpace(machineID) == "" {
		return fmt.Errorf("%w: this gateway cannot read its own machine id", ErrWrongMachine)
	}
	if request.MachineID != machineID {
		return ErrWrongMachine
	}

	issued := time.Unix(request.IssuedAt, 0)
	if difference := now.Sub(issued); difference > FreshnessWindow || difference < -FreshnessWindow {
		return fmt.Errorf("%w: signed %s away from this clock", ErrStale, difference.Round(time.Second))
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKeyBase64))
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		// No usable key means nothing can be authenticated, so nothing is
		// answered. Reported as a signature failure rather than as the
		// request's fault, because the request may well have been fine.
		return fmt.Errorf("%w: no usable issuer public key", ErrBadSignature)
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(request.Signature))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature is not %d bytes", ErrBadSignature, ed25519.SignatureSize)
	}
	message, err := request.canonical()
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(decoded), message, signature) {
		return ErrBadSignature
	}
	return nil
}

// Sign produces a signed request.
//
// The gateway never calls this -- it holds no private key. It exists for the
// issuing console and for tests, which need requests that genuinely verify
// rather than fixtures asserting against themselves.
func Sign(
	nonce, machineID string,
	issuedAt time.Time,
	privateKey ed25519.PrivateKey,
) (Request, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return Request{}, fmt.Errorf("%w: signing key is not %d bytes",
			ErrMalformed, ed25519.PrivateKeySize)
	}
	request := Request{
		Type:      Type,
		Version:   Version,
		Nonce:     nonce,
		MachineID: machineID,
		IssuedAt:  issuedAt.Unix(),
	}
	message, err := request.canonical()
	if err != nil {
		return Request{}, err
	}
	request.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, message))
	return request, nil
}
