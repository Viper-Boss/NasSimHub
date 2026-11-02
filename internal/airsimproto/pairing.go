package airsimproto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"
)

// PairingLifetime is how long a pairing window stays open, as on Android.
const PairingLifetime = 120 * time.Second

var (
	ErrPairingExpired = errors.New("airsim: pairing window expired")
	ErrPairingUsed    = errors.New("airsim: pairing window already used")
)

// PairingSession is one short-lived pairing window: a random session ID, a
// six-digit code shown on the gateway's screen and an ephemeral X25519 key.
// The app seals its registration (including the VoWLAN secret) to that key
// with HKDF-SHA256(salt=session, info="AirSIMPair/v1:"+code) and AES-256-GCM.
type PairingSession struct {
	ID        string
	Code      string
	ExpiresAt time.Time

	private *ecdh.PrivateKey

	mu   sync.Mutex
	used bool
}

func NewPairingSession(now time.Time) (*PairingSession, error) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate pairing key: %w", err)
	}
	code, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return nil, fmt.Errorf("generate pairing code: %w", err)
	}
	id, err := randomUUID()
	if err != nil {
		return nil, err
	}
	return &PairingSession{
		ID:        id,
		Code:      fmt.Sprintf("%06d", code.Int64()),
		ExpiresAt: now.Add(PairingLifetime),
		private:   private,
	}, nil
}

// PublicKey is the raw 32-byte X25519 public key.
func (s *PairingSession) PublicKey() []byte {
	return s.private.PublicKey().Bytes()
}

// Open decrypts a claim. A session can be opened successfully only once; a
// wrong code leaves it usable until it expires, as on Android.
func (s *PairingSession) Open(clientPublicKey, sealed []byte, now time.Time) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used {
		return nil, ErrPairingUsed
	}
	if now.After(s.ExpiresAt) {
		return nil, ErrPairingExpired
	}
	plaintext, err := OpenSealedRegistration(s.private, clientPublicKey, sealed, s.ID, s.Code)
	if err != nil {
		return nil, err
	}
	s.used = true
	return plaintext, nil
}

// OpenSealedRegistration is the receiving half of SamsungPairingCrypto.seal.
func OpenSealedRegistration(private *ecdh.PrivateKey, clientPublicKey, sealed []byte, sessionID, code string) ([]byte, error) {
	key, err := deriveKey(private, clientPublicKey, sessionID, code)
	if err != nil {
		return nil, err
	}
	if len(sealed) < 12+16 {
		return nil, ErrAuthentication
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, sealed[:12], sealed[12:], []byte(sessionID))
	if err != nil {
		return nil, ErrAuthentication
	}
	return plaintext, nil
}

// SealRegistration is the app's side; the gateway uses it only in tests.
func SealRegistration(clientPrivate *ecdh.PrivateKey, serverPublicKey []byte, registration []byte, sessionID, code string, nonce []byte) ([]byte, error) {
	key, err := deriveKey(clientPrivate, serverPublicKey, sessionID, code)
	if err != nil {
		return nil, err
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if nonce == nil {
		nonce = make([]byte, 12)
		if _, err := rand.Read(nonce); err != nil {
			return nil, err
		}
	}
	return aead.Seal(append([]byte(nil), nonce...), nonce, registration, []byte(sessionID)), nil
}

func deriveKey(private *ecdh.PrivateKey, peerPublic []byte, sessionID, code string) ([]byte, error) {
	if len(code) != 6 || sessionID == "" {
		return nil, ErrInvalid
	}
	for _, digit := range code {
		if digit < '0' || digit > '9' {
			return nil, ErrInvalid
		}
	}
	peer, err := ecdh.X25519().NewPublicKey(peerPublic)
	if err != nil {
		return nil, ErrAuthentication
	}
	shared, err := private.ECDH(peer)
	if err != nil {
		return nil, ErrAuthentication
	}
	return hkdfKey(shared, sessionID, code)
}

func hkdfKey(shared []byte, sessionID, code string) ([]byte, error) {
	return hkdf.Key(sha256.New, shared, []byte(sessionID), "AirSIMPair/v1:"+code, 32)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate pairing session id: %w", err)
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	text := hex.EncodeToString(value[:])
	return text[0:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:32], nil
}
