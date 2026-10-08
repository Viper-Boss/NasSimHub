// Package pqsig is the gateway's one doorway to ML-DSA signatures.
//
// The implementation is the standard library's crypto/mldsa, reached through
// the Node protocol module that already wraps it for device identities. This
// package exists so the code that decides what an update installs
// (internal/release, cmd/licensegen) depends on a small local interface
// instead of importing that module directly.
package pqsig

import (
	"errors"
	"fmt"

	"github.com/human-agent65535/nassimhub-node/proto"
)

// MLDSA87 is the algorithm releases are signed with.
const MLDSA87 = string(proto.PQSigMLDSA87)

// SeedSize is the size of an ML-DSA private seed.
const SeedSize = proto.MLDSASeedSize

// RequiredToolchain names the Go release that has crypto/mldsa.
const RequiredToolchain = proto.GoVersionForMLDSA

// ErrUnavailable means this build has no ML-DSA implementation. It is never a
// reason to treat a signature as valid.
var ErrUnavailable = errors.New("pqsig: this build has no ML-DSA implementation")

// Available reports whether this build can sign and verify ML-DSA.
func Available() bool { return proto.EnableStandardPQ() }

// Verify checks one signature. Any failure - including a build that cannot
// check - is an error.
func Verify(algorithm string, publicKey, message, signature []byte) error {
	if !Available() {
		return fmt.Errorf("%w; build with %s or newer", ErrUnavailable, RequiredToolchain)
	}
	verifier, err := proto.PQVerifierFor(proto.PQSignatureAlgorithm(algorithm))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return verifier.Verify(publicKey, message, signature)
}

// Signer signs with one ML-DSA key.
type Signer interface {
	Algorithm() string
	PublicKey() []byte
	Sign(message []byte) ([]byte, error)
}

type signer struct{ inner *proto.MLDSASigner }

func (s signer) Algorithm() string                   { return string(s.inner.Algorithm()) }
func (s signer) PublicKey() []byte                   { return s.inner.PublicKey() }
func (s signer) Sign(message []byte) ([]byte, error) { return s.inner.Sign(message) }

// NewSeed creates a private seed.
func NewSeed() ([]byte, error) {
	if !Available() {
		return nil, fmt.Errorf("%w; build with %s or newer", ErrUnavailable, RequiredToolchain)
	}
	return proto.NewMLDSASeed()
}

// NewSigner builds a signer from a seed.
func NewSigner(algorithm string, seed []byte) (Signer, error) {
	if !Available() {
		return nil, fmt.Errorf("%w; build with %s or newer", ErrUnavailable, RequiredToolchain)
	}
	inner, err := proto.NewMLDSASigner(proto.PQSignatureAlgorithm(algorithm), seed)
	if err != nil {
		return nil, err
	}
	return signer{inner: inner}, nil
}
