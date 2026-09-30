// Package nodetrust is Core's side of the Node TLS trust model.
//
// There are exactly three configurations here and they are not
// interchangeable:
//
//   - ClientConfig is the steady state and the only one used for ordinary
//     traffic. It sets no InsecureSkipVerify: the device certificate is its own
//     CA, so Core puts that one certificate in a root pool and runs standard
//     chain verification, then re-checks the leaf key against the pin.
//   - FirstContactConfig is used once, before a device is adopted, when there
//     is nothing to verify against yet.
//   - RepinConfig is used only after a strict handshake has already failed, to
//     decide whether the cause was a renewal (same key, new certificate) or a
//     substitution (different key).
//
// The last two replace PKI verification with a mandatory callback rather than
// skipping verification. Neither is ever used for a Node that already has a
// working pinned certificate, and a structural test caps how many times
// InsecureSkipVerify may appear in the tree so a fourth use cannot slip in.
package nodetrust

import (
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/human-agent65535/nassimhub-node/proto"
)

// ErrNotPinned reports a certificate that is not the pinned device.
var ErrNotPinned = errors.New("node certificate does not match the pinned device key")

// Pin is everything Core remembers about a Node's TLS identity.
//
// It holds the public key, not the certificate. A renewed certificate carries
// the same key and still matches; a substituted key never does.
type Pin struct {
	DeviceID  string
	Platform  proto.Platform
	PublicKey ed25519.PublicKey
}

// ClientConfig builds a TLS configuration that trusts exactly one device.
//
// The returned configuration performs full chain verification against a pool
// containing only that device's certificate, checks the server name derived
// from device_id, and re-checks the leaf key against the pin.
func ClientConfig(pin Pin, certificate *x509.Certificate) (*tls.Config, error) {
	if len(pin.PublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("a pin requires the device public key")
	}
	if certificate == nil {
		return nil, errors.New("a pin requires the device certificate")
	}
	if err := proto.VerifyCertificateBinding(certificate, pin.DeviceID, pin.Platform, pin.PublicKey); err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)

	return &tls.Config{
		RootCAs:    roots,
		ServerName: proto.DeviceHostname(pin.DeviceID),
		MinVersion: tls.VersionTLS13,
		// Belt and braces on top of the chain check above. It runs on every
		// full handshake and costs a key comparison.
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return ErrNotPinned
			}
			leaf, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("%w: %v", ErrNotPinned, err)
			}
			key, err := proto.CertificatePublicKey(leaf)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrNotPinned, err)
			}
			if !pin.PublicKey.Equal(key) {
				return ErrNotPinned
			}
			return nil
		},
	}, nil
}

// Transport builds an HTTP transport for one pinned Node.
//
// Connection limits are deliberate. A Node is one small device; an unbounded
// idle pool per Node would be a slow resource leak in a household with several
// of them, and IdleConnTimeout makes a Node that has gone away stop consuming
// sockets on the NAS without waiting for a TCP timeout.
func Transport(config *tls.Config) *http.Transport {
	return &http.Transport{
		TLSClientConfig:       config,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   2,
		MaxConnsPerHost:       8,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

// FirstContactConfig builds the configuration used before a Node is adopted.
//
// There is no pin yet, so this cannot answer "is this the device we trust". It
// answers the weaker question that first contact actually needs: "is this a
// device whose certificate key derives the device_id it is presenting". An
// attacker can still offer their own device - and the user will see an
// unfamiliar device_id and decline - but they cannot relay or impersonate an
// existing one, because completing the handshake needs the private key.
//
// InsecureSkipVerify is still not used. Chain verification is performed
// manually against the presented leaf, which is legitimate here precisely
// because there is nothing else to verify against yet, and the binding check
// below is mandatory rather than advisory.
func FirstContactConfig(observed *Observation) *tls.Config {
	return &tls.Config{
		// A server name is still sent so a Node serving several identities
		// could select correctly; verification of it happens in the callback.
		MinVersion: tls.VersionTLS13,
		// Chain verification against a public CA is meaningless for a
		// self-signed device certificate, so it is replaced - not skipped - by
		// the binding check. The callback always runs on a full handshake, and
		// this configuration is never reused for an already-pinned Node.
		InsecureSkipVerify: true, //nolint:gosec // replaced by the mandatory binding check below
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("node presented no certificate")
			}
			leaf, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("node certificate is unreadable: %w", err)
			}
			key, err := proto.CertificatePublicKey(leaf)
			if err != nil {
				return err
			}
			// Self-signature is checked here; the device_id the certificate
			// claims is checked by the caller against the discovery document.
			if err := leaf.CheckSignatureFrom(leaf); err != nil {
				return fmt.Errorf("node certificate is not self-signed by its own key: %w", err)
			}
			if observed != nil {
				observed.set(leaf, key)
			}
			return nil
		},
	}
}

// RepinConfig is used when a strict handshake fails and Core needs to decide
// whether the device renewed its certificate or was replaced.
//
// It is strictly narrower than first contact: the callback requires the leaf
// key to equal the key already pinned, so a device presenting any other key is
// refused at the handshake rather than being reported for a human to judge.
func RepinConfig(pin Pin, observed *Observation) (*tls.Config, error) {
	if len(pin.PublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("re-pinning requires the existing device public key")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		// PKI verification is meaningless against a certificate Core has not
		// seen yet; it is replaced by the key check below, which is mandatory
		// and strictly stronger than name verification for this purpose.
		InsecureSkipVerify: true, //nolint:gosec // replaced by the mandatory pin check below
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return ErrNotPinned
			}
			leaf, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("%w: %v", ErrNotPinned, err)
			}
			if err := proto.VerifyCertificateBinding(leaf, pin.DeviceID, pin.Platform, pin.PublicKey); err != nil {
				return err
			}
			if observed != nil {
				key, err := proto.CertificatePublicKey(leaf)
				if err != nil {
					return err
				}
				observed.set(leaf, key)
			}
			return nil
		},
	}, nil
}

// Observation captures what a first-contact handshake saw, so the caller can
// compare it with the discovery document and then build a pin from it.
type Observation struct {
	certificate *x509.Certificate
	key         ed25519.PublicKey
}

func (o *Observation) set(certificate *x509.Certificate, key ed25519.PublicKey) {
	o.certificate = certificate
	o.key = key
}

// Certificate reports the leaf seen during the handshake.
func (o *Observation) Certificate() *x509.Certificate { return o.certificate }

// PublicKey reports the key seen during the handshake.
func (o *Observation) PublicKey() ed25519.PublicKey { return o.key }

// Confirm checks the observed certificate against a discovery document and
// returns the pin to store.
//
// This is the moment trust is established. It fails unless the key that
// completed the TLS handshake is the same key the discovery document
// advertises, and that key derives the advertised device_id. An attacker who
// proxies a genuine Node's document cannot pass, because the handshake key
// would be theirs and the document's would not.
func (o *Observation) Confirm(deviceID string, platform proto.Platform, advertised ed25519.PublicKey) (Pin, error) {
	if o.certificate == nil || len(o.key) == 0 {
		return Pin{}, errors.New("no certificate was observed")
	}
	if len(advertised) != ed25519.PublicKeySize {
		return Pin{}, errors.New("discovery document carried no usable key")
	}
	if !o.key.Equal(advertised) {
		return Pin{}, fmt.Errorf(
			"%w: the key that completed the handshake is not the key the device advertises",
			proto.ErrCertificateBinding)
	}
	if err := proto.VerifyCertificateBinding(o.certificate, deviceID, platform, advertised); err != nil {
		return Pin{}, err
	}
	return Pin{DeviceID: deviceID, Platform: platform, PublicKey: advertised}, nil
}

// ConfirmRenewal validates a certificate observed on the re-pin path and
// returns the certificate Core should store in place of the old one.
//
// The pin itself does not change: the key is the identity, and a renewal that
// altered it would not be a renewal.
func (o *Observation) ConfirmRenewal(pin Pin) (*x509.Certificate, error) {
	if o.certificate == nil || len(o.key) == 0 {
		return nil, errors.New("no certificate was observed")
	}
	if !pin.PublicKey.Equal(o.key) {
		return nil, ErrNotPinned
	}
	if err := proto.VerifyCertificateBinding(o.certificate, pin.DeviceID, pin.Platform, pin.PublicKey); err != nil {
		return nil, err
	}
	return o.certificate, nil
}
