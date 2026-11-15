// SPDX-License-Identifier: AGPL-3.0-only
package forwarding

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
)

type SecretBox interface {
	Seal([]byte, []byte) ([]byte, []byte, error)
	Open([]byte, []byte, []byte) ([]byte, error)
}

func secretAD(ch Channel, key string) []byte {
	return []byte("nassimhub-forwarding-v1\x00" + ch.ID + "\x00" + ch.Type + "\x00" + key)
}
func SealConfig(next, prior Config, box SecretBox) (Config, error) {
	previous := map[string]Channel{}
	for _, ch := range prior.Channels {
		previous[ch.ID] = ch
	}
	for i, ch := range next.Channels {
		sealed := map[string]Secret{}
		for k, s := range ch.Secrets {
			if len(s.Nonce) > 0 || len(s.Ciphertext) > 0 {
				return Config{}, ErrInvalid
			}
			switch {
			case s.Env != "":
				if s.Value != "" || !envName.MatchString(s.Env) {
					return Config{}, ErrInvalid
				}
				sealed[k] = Secret{Env: s.Env}
			case s.Value != "":
				if box == nil {
					return Config{}, ErrUnavailable
				}
				nonce, ciphertext, err := box.Seal([]byte(s.Value), secretAD(ch, k))
				if err != nil {
					return Config{}, ErrUnavailable
				}
				sealed[k] = Secret{Nonce: nonce, Ciphertext: ciphertext}
			case s.Set:
				old := previous[ch.ID]
				if old.Type != ch.Type || len(old.Secrets[k].Ciphertext) == 0 {
					return Config{}, ErrInvalid
				}
				sealed[k] = old.Secrets[k]
			default:
				sealed[k] = Secret{}
			}
		}
		next.Channels[i].Secrets = sealed
	}
	if err := next.Validate(); err != nil {
		return Config{}, err
	}
	return next, nil
}

type Dispatcher struct {
	// Test-only trust roots; normal construction uses the system certificate store.
	tlsRoots  *x509.CertPool
	Secrets   SecretBox
	LookupEnv func(string) (string, bool)
	Transport func(context.Context) http.RoundTripper
	SMS       func(context.Context, string, string, string, string) error
}

func (d *Dispatcher) secret(ch Channel, key string) (string, error) {
	s := ch.Secrets[key]
	if s.Env != "" {
		lookup := d.LookupEnv
		if lookup == nil {
			lookup = os.LookupEnv
		}
		value, ok := lookup(s.Env)
		if !ok || len(value) == 0 || len(value) > 16384 {
			return "", ErrDenied
		}
		return value, nil
	}
	if len(s.Ciphertext) > 0 && d.Secrets != nil {
		raw, err := d.Secrets.Open(s.Nonce, s.Ciphertext, secretAD(ch, key))
		if err != nil || len(raw) == 0 {
			return "", ErrDenied
		}
		return string(raw), nil
	}
	return "", ErrDenied
}
func taskRequestID(id int64) string { return "forwarding_" + strconv.FormatInt(id, 10) }
func jsonText(v any) string         { raw, _ := json.Marshal(v); return string(raw) }
func integer(p map[string]string, key string, defaultValue int) (int, error) {
	if p[key] == "" {
		return defaultValue, nil
	}
	n, err := strconv.Atoi(p[key])
	if err != nil {
		return 0, fmt.Errorf("%w: %s", ErrDenied, key)
	}
	return n, nil
}
