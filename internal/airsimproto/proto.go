// Package airsimproto implements the host side of the AirSIM iPhone app's
// local ("VoWLAN") protocol, so a NasSimHub gateway can stand in for the
// Samsung phone the app was written for.
//
// The wire formats here are fixed by the AirSIM app and its Android gateway
// (github.com/kai-wu-cortex/AirSIM, iOS/AirSIM/VoWLANAuth.swift,
// SamsungPairingCrypto.swift and android/.../VoWLAN*.java). Nothing in this
// package may change a byte of them: an unmodified iPhone app has to accept
// every response. See docs/AIRSIM-COMPAT.md for the full description.
package airsimproto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// ClockSkew is how far a signed request's timestamp may be from the
	// gateway clock, as in the Android gateway.
	ClockSkew = 30 * time.Second
	// MinSecretBytes is the shortest VoWLAN secret the app ever generates.
	MinSecretBytes = 32

	HeaderVersion   = "X-AirSIM-VoWLAN-Version"
	HeaderTimestamp = "X-AirSIM-VoWLAN-Timestamp"
	HeaderNonce     = "X-AirSIM-VoWLAN-Nonce"
	HeaderSignature = "X-AirSIM-VoWLAN-Signature"

	// PCMMagic opens a PCM connection; PCMReady is the gateway's reply.
	PCMMagic = "AIRSIMVWL1"
	PCMReady = "AIRSIMREADY"

	// PCM audio is 8 kHz mono signed 16-bit little endian in 20 ms frames.
	PCMSampleRate = 8000
	PCMFrameBytes = 320
)

var (
	ErrAuthentication = errors.New("airsim: authentication failed")
	ErrInvalid        = errors.New("airsim: invalid request")
)

// Base64URL encodes without padding, the encoding every AirSIM field uses.
func Base64URL(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

// DecodeBase64URL accepts the unpadded form and, like the app, padded input.
func DecodeBase64URL(value string) ([]byte, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "=")
	return base64.RawURLEncoding.DecodeString(value)
}

// Canonical is the string a VoWLAN request signature covers:
// METHOD\npath?query\nsha256-hex(body)\ntimestamp\nnonce.
func Canonical(method, pathAndQuery string, body []byte, timestamp int64, nonce string) string {
	digest := sha256.Sum256(body)
	return strings.ToUpper(method) + "\n" + pathAndQuery + "\n" + hex.EncodeToString(digest[:]) +
		"\n" + strconv.FormatInt(timestamp, 10) + "\n" + nonce
}

// Sign returns base64url(HMAC-SHA256(secret, message)).
func Sign(secret []byte, message string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(message))
	return Base64URL(mac.Sum(nil))
}

// VerifySignature compares in constant time.
func VerifySignature(secret []byte, signature, message string) bool {
	if len(secret) < MinSecretBytes || signature == "" {
		return false
	}
	expected := Sign(secret, message)
	return hmac.Equal([]byte(expected), []byte(signature))
}

// FreshTimestamp reports whether timestamp (Unix seconds) is within ClockSkew.
func FreshTimestamp(timestamp int64, now time.Time) bool {
	delta := now.Unix() - timestamp
	if delta < 0 {
		delta = -delta
	}
	return time.Duration(delta)*time.Second <= ClockSkew
}

// ValidNonce mirrors the Android gateway's length limits.
func ValidNonce(nonce string) bool {
	return len(nonce) >= 7 && len(nonce) <= 128
}

// HealthProof proves to the app that the gateway holds its pairing secret.
func HealthProof(secret []byte, nonce, agentID string) string {
	return Sign(secret, "AIRSIM-HEALTH1\n"+nonce+"\n"+agentID)
}

// DialProof binds an owned outgoing call to the request that created it.
func DialProof(secret []byte, nonce, operationID, androidID, backendID string) string {
	return Sign(secret, "AIRSIM-DIAL1\n"+nonce+"\n"+operationID+"\n"+androidID+"\n"+backendID)
}

// DirectRouteProof lets a manually paired app trust the returned ports.
func DirectRouteProof(secret []byte, sessionID, clientKey, agentID string, controlPort, pcmPort int) string {
	return Sign(secret, "AIRSIM-DIRECT1\n"+sessionID+"\n"+clientKey+"\n"+agentID+"\n"+
		strconv.Itoa(controlPort)+"\n"+strconv.Itoa(pcmPort))
}

// ReplayCache remembers nonces for twice the clock skew.
type ReplayCache struct {
	mu        sync.Mutex
	retention time.Duration
	seen      map[string]time.Time
}

func NewReplayCache() *ReplayCache {
	return &ReplayCache{retention: 2 * ClockSkew, seen: make(map[string]time.Time)}
}

// Accept records nonce and reports whether it was new.
func (c *ReplayCache) Accept(nonce string, now time.Time) bool {
	if !ValidNonce(nonce) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, at := range c.seen {
		if now.Sub(at) > c.retention {
			delete(c.seen, key)
		}
	}
	if _, exists := c.seen[nonce]; exists {
		return false
	}
	if len(c.seen) >= 100_000 {
		return false
	}
	c.seen[nonce] = now
	return true
}

// PCMPreface is the first line of a PCM connection:
// "AIRSIMVWL1 <timestamp> <nonce> <signature>\n".
type PCMPreface struct {
	Timestamp int64
	Nonce     string
	Signature string
}

func ParsePCMPreface(line string) (PCMPreface, error) {
	if len(line) > 1024 || !strings.HasSuffix(line, "\n") {
		return PCMPreface{}, ErrInvalid
	}
	fields := strings.Split(strings.TrimSuffix(line, "\n"), " ")
	if len(fields) != 4 || fields[0] != PCMMagic ||
		len(fields[2]) < 7 || len(fields[2]) > 128 ||
		len(fields[3]) < 40 || len(fields[3]) > 128 {
		return PCMPreface{}, ErrInvalid
	}
	timestamp, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return PCMPreface{}, ErrInvalid
	}
	return PCMPreface{Timestamp: timestamp, Nonce: fields[2], Signature: fields[3]}, nil
}

// PCMCanonical is what a PCM preface signature covers.
func PCMCanonical(preface PCMPreface) string {
	return Canonical("PCM", "/v1/pcm", nil, preface.Timestamp, preface.Nonce)
}
