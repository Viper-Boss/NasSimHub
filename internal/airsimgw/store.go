package airsimgw

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// SecretBox seals the per-iPhone VoWLAN secrets at rest. It is the same
// settings-key box the API uses for SIM PINs (internal/secretbox.Box).
type SecretBox interface {
	Seal(plaintext, associatedData []byte) (nonce, ciphertext []byte, err error)
	Open(nonce, ciphertext, associatedData []byte) ([]byte, error)
}

const (
	maxPairings       = 16
	maxAckedMessages  = 512
	secretAssociation = "airsim-pairing:"
)

var (
	ErrPairingNotFound = errors.New("airsim: pairing not found")
	ErrTooManyPairings = errors.New("airsim: too many paired iPhones")
)

// Pairing is one paired iPhone. The VoWLAN secret never leaves this package:
// the API and the settings page only ever see the metadata.
type Pairing struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	LineID     string    `json:"line_id"`
	DeviceHint string    `json:"device_hint,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at,omitempty"`
	LastPeer   string    `json:"last_peer,omitempty"`

	secret []byte
}

type sealedSecret struct {
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

type storedPairing struct {
	Pairing
	Secret sealedSecret `json:"secret"`
	// AckedMessages are the delivery IDs the app has confirmed, newest last.
	AckedMessages []string `json:"acked_messages,omitempty"`
}

type storedState struct {
	Version  int             `json:"version"`
	AgentID  string          `json:"agent_id"`
	Enabled  bool            `json:"enabled"`
	LineID   string          `json:"line_id,omitempty"`
	Pairings []storedPairing `json:"pairings"`
}

// store keeps the gateway state in one JSON file next to the other homesim
// settings files (no database migration: the schema there is append-only and
// this feature must be removable).
type store struct {
	path    string
	secrets SecretBox

	mu    sync.Mutex
	state storedState
	// open secrets, by pairing ID
	keys  map[string][]byte
	acked map[string]map[string]bool
}

func openStore(path string, secrets SecretBox) (*store, error) {
	if secrets == nil {
		return nil, errors.New("airsim: settings secret box is required")
	}
	s := &store{
		path:    path,
		secrets: secrets,
		keys:    map[string][]byte{},
		acked:   map[string]map[string]bool{},
	}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.state = storedState{Version: 1}
	case err != nil:
		return nil, fmt.Errorf("read AirSIM state: %w", err)
	default:
		if err := json.Unmarshal(data, &s.state); err != nil {
			return nil, fmt.Errorf("decode AirSIM state: %w", err)
		}
	}
	if s.state.AgentID == "" {
		var raw [8]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, err
		}
		s.state.AgentID = "nassimhub-" + hex.EncodeToString(raw[:])
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	kept := s.state.Pairings[:0]
	for _, record := range s.state.Pairings {
		secret, err := secrets.Open(record.Secret.Nonce, record.Secret.Ciphertext, []byte(secretAssociation+record.ID))
		if err != nil || len(secret) < 32 {
			// A secret that no longer opens (settings key rotated) is useless:
			// the iPhone has to pair again.
			continue
		}
		s.keys[record.ID] = secret
		acked := map[string]bool{}
		for _, id := range record.AckedMessages {
			acked[id] = true
		}
		s.acked[record.ID] = acked
		kept = append(kept, record)
	}
	s.state.Pairings = kept
	return s, nil
}

func (s *store) agentID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.AgentID
}

func (s *store) settings() (enabled bool, lineID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Enabled, s.state.LineID
}

func (s *store) setSettings(enabled bool, lineID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Enabled = enabled
	s.state.LineID = lineID
	return s.saveLocked()
}

func (s *store) list() []Pairing {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Pairing, 0, len(s.state.Pairings))
	for _, record := range s.state.Pairings {
		result = append(result, record.Pairing)
	}
	return result
}

// withSecrets returns every pairing with its secret, for signature checks.
func (s *store) withSecrets() []Pairing {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Pairing, 0, len(s.state.Pairings))
	for _, record := range s.state.Pairings {
		pairing := record.Pairing
		pairing.secret = s.keys[record.ID]
		result = append(result, pairing)
	}
	return result
}

func (s *store) add(pairing Pairing, secret []byte) (Pairing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.state.Pairings) >= maxPairings {
		return Pairing{}, ErrTooManyPairings
	}
	if pairing.ID == "" {
		var raw [12]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return Pairing{}, err
		}
		pairing.ID = hex.EncodeToString(raw[:])
	}
	nonce, ciphertext, err := s.secrets.Seal(secret, []byte(secretAssociation+pairing.ID))
	if err != nil {
		return Pairing{}, fmt.Errorf("seal AirSIM secret: %w", err)
	}
	s.state.Pairings = append(s.state.Pairings, storedPairing{
		Pairing: pairing,
		Secret:  sealedSecret{Nonce: nonce, Ciphertext: ciphertext},
	})
	s.keys[pairing.ID] = slices.Clone(secret)
	s.acked[pairing.ID] = map[string]bool{}
	if err := s.saveLocked(); err != nil {
		s.state.Pairings = s.state.Pairings[:len(s.state.Pairings)-1]
		delete(s.keys, pairing.ID)
		delete(s.acked, pairing.ID)
		return Pairing{}, err
	}
	return pairing, nil
}

func (s *store) remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := slices.IndexFunc(s.state.Pairings, func(record storedPairing) bool { return record.ID == id })
	if index < 0 {
		return ErrPairingNotFound
	}
	removed := s.state.Pairings[index]
	s.state.Pairings = slices.Delete(s.state.Pairings, index, index+1)
	if err := s.saveLocked(); err != nil {
		s.state.Pairings = slices.Insert(s.state.Pairings, index, removed)
		return err
	}
	clear(s.keys[id])
	delete(s.keys, id)
	delete(s.acked, id)
	return nil
}

// removeUser drops every pairing a deleted or demoted account made.
func (s *store) removeUser(userID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := len(s.state.Pairings)
	var removed []string
	s.state.Pairings = slices.DeleteFunc(s.state.Pairings, func(record storedPairing) bool {
		if record.UserID == userID {
			removed = append(removed, record.ID)
			return true
		}
		return false
	})
	if len(removed) == 0 {
		return 0, nil
	}
	if err := s.saveLocked(); err != nil {
		return 0, err
	}
	for _, id := range removed {
		clear(s.keys[id])
		delete(s.keys, id)
		delete(s.acked, id)
	}
	return before - len(s.state.Pairings), nil
}

// touch records activity. It is written to disk at most once a minute.
func (s *store) touch(id, peer string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.state.Pairings {
		record := &s.state.Pairings[index]
		if record.ID != id {
			continue
		}
		persist := now.Sub(record.LastSeenAt) > time.Minute || record.LastPeer != peer
		record.LastSeenAt = now.UTC()
		record.LastPeer = peer
		if persist {
			_ = s.saveLocked()
		}
		return
	}
}

func (s *store) isAcked(id, deliveryID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acked[id][deliveryID]
}

func (s *store) ack(id string, deliveryIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.state.Pairings {
		record := &s.state.Pairings[index]
		if record.ID != id {
			continue
		}
		acked := s.acked[id]
		changed := false
		for _, deliveryID := range deliveryIDs {
			if deliveryID == "" || len(deliveryID) > 128 || acked[deliveryID] {
				continue
			}
			acked[deliveryID] = true
			record.AckedMessages = append(record.AckedMessages, deliveryID)
			changed = true
		}
		if extra := len(record.AckedMessages) - maxAckedMessages; extra > 0 {
			for _, old := range record.AckedMessages[:extra] {
				delete(acked, old)
			}
			record.AckedMessages = slices.Clone(record.AckedMessages[extra:])
		}
		if !changed {
			return nil
		}
		return s.saveLocked()
	}
	return ErrPairingNotFound
}

func (s *store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, s.path)
}
