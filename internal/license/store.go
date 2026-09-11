package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Where certificates live, and how they become an answer to "may I".
//
// Two directories, and the split is not tidiness. A MACHINE certificate stays
// with the gateway, so it belongs in the gateway's own data directory. A MODULE
// certificate follows the modem: the customer who paid for outbound calling on
// a particular SIM expects that to keep working when they move the module to
// another box. Keeping them apart is what makes "move the module, keep the
// licence" a file copy rather than a support ticket.
//
// Everything here is plain JSON, unencrypted and unobfuscated. That is
// deliberate and the reasoning is in certificate.go: the signature is what
// makes a certificate unforgeable, and hiding it would buy nothing against the
// only attacker who matters while making every support call harder.

const (
	// machineDirectory holds the certificate bound to this gateway.
	machineDirectory = "license/machine"
	// moduleDirectory holds certificates bound to modem serial numbers, one
	// file per module so a module can be moved by copying one file.
	moduleDirectory = "license/modules"
	// fingerprintFile records the identifiers seen at first activation.
	fingerprintFile = "license/fingerprint.json"

	// maxCertificateBytes bounds one file. A certificate is a few hundred
	// bytes; anything near this is not one.
	maxCertificateBytes = 8 * 1024
	// maxCertificates bounds how many are read, so a directory somebody
	// filled cannot make every feature check slow.
	maxCertificates = 64
)

// Store reads certificates and evaluates entitlements.
//
// It caches, because Check is called on the path of every outbound message and
// every dial, and reading a directory per call would put disk I/O in the way of
// placing a phone call. The cache is short: a certificate that arrives over NKN
// has to take effect without a restart, and a customer who has just paid will
// retry within seconds.
type Store struct {
	root      string
	publicKey ed25519.PublicKey

	mu       sync.Mutex
	cached   Entitlements
	cachedAt time.Time
	// modules is the module set the cache was computed for. A module being
	// plugged in or pulled out changes the answer, so it invalidates the cache
	// even inside the TTL.
	modules string
	// now is a seam for tests.
	now func() time.Time
}

// cacheTTL is how long an evaluation is reused.
const cacheTTL = 15 * time.Second

// NewStore builds a store over a data directory.
//
// An empty or malformed public key is not an error here. It means this build
// cannot verify anything, so every paid feature stays locked -- which is the
// correct behaviour for a build with no issuer key, and far better than
// refusing to start and taking the free features down with it.
func NewStore(dataDirectory, issuerPublicKey string) *Store {
	store := &Store{root: strings.TrimSpace(dataDirectory), now: time.Now}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(issuerPublicKey))
	if err == nil && len(decoded) == ed25519.PublicKeySize {
		store.publicKey = ed25519.PublicKey(decoded)
	}
	return store
}

// Configured reports whether this build can verify certificates at all.
//
// The settings page needs this to tell "you have not bought anything" apart
// from "this build cannot check licences", which are very different messages to
// show somebody who has paid.
func (s *Store) Configured() bool {
	return s != nil && len(s.publicKey) == ed25519.PublicKeySize
}

// machinePath and modulePath resolve storage locations.
func (s *Store) machinePath() string {
	return filepath.Join(s.root, machineDirectory, "certificate.json")
}

func (s *Store) modulePath(serial string) string {
	// The serial goes into a filename, so it is reduced to characters that
	// cannot escape the directory. A certificate whose serial does not survive
	// this is refused on write rather than written somewhere unexpected.
	return filepath.Join(s.root, moduleDirectory, safeSerial(serial)+".json")
}

// safeSerial keeps a serial number usable as a filename.
func safeSerial(serial string) string {
	var builder strings.Builder
	for _, symbol := range strings.TrimSpace(serial) {
		switch {
		case symbol >= '0' && symbol <= '9',
			symbol >= 'a' && symbol <= 'z',
			symbol >= 'A' && symbol <= 'Z',
			symbol == '-', symbol == '_':
			builder.WriteRune(symbol)
		default:
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

// Save writes a certificate to the right place for its scope.
//
// It verifies BEFORE writing. Storing an unverifiable certificate would leave a
// file that looks like a licence, reports as rejected on every read, and gives
// the customer something to point at while nothing works -- so a bad
// certificate is refused at the door with a reason.
func (s *Store) Save(signed Signed, now time.Time) error {
	if s == nil || s.root == "" {
		return fmt.Errorf("license: no storage directory is configured")
	}
	if !s.Configured() {
		return fmt.Errorf("license: this build has no issuer public key, so no certificate can be verified")
	}
	if err := Verify(signed, s.publicKey, now); err != nil {
		return err
	}

	var path string
	switch signed.Certificate.Scope {
	case ScopeMachine:
		path = s.machinePath()
	case ScopeModule:
		serial := strings.TrimSpace(signed.Certificate.ModuleSN)
		if safeSerial(serial) == "" {
			return fmt.Errorf("%w: module serial number is unusable as a filename", ErrMalformed)
		}
		path = s.modulePath(serial)
	default:
		return fmt.Errorf("%w: unknown scope %q", ErrMalformed, signed.Certificate.Scope)
	}

	encoded, err := json.MarshalIndent(signed, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Written through a temporary file: a certificate half-written by a crash
	// would be unparseable, and the customer would have paid for a file that
	// reads as corrupt.
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o644); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	s.invalidate()
	return nil
}

// invalidate drops the cache so the next check sees a new certificate.
func (s *Store) invalidate() {
	s.mu.Lock()
	s.cachedAt = time.Time{}
	s.mu.Unlock()
}

// Certificates reads every stored certificate, valid or not.
func (s *Store) Certificates() []Signed {
	if s == nil || s.root == "" {
		return nil
	}
	found := []Signed{}
	if signed, ok := readCertificate(s.machinePath()); ok {
		found = append(found, signed)
	}
	entries, err := os.ReadDir(filepath.Join(s.root, moduleDirectory))
	if err != nil {
		return found
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		names = append(names, entry.Name())
	}
	// Sorted so the evaluation is deterministic; directory order is not.
	sort.Strings(names)
	for _, name := range names {
		if len(found) >= maxCertificates {
			break
		}
		if signed, ok := readCertificate(filepath.Join(s.root, moduleDirectory, name)); ok {
			found = append(found, signed)
		}
	}
	return found
}

func readCertificate(path string) (Signed, bool) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxCertificateBytes {
		return Signed{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Signed{}, false
	}
	var signed Signed
	if err := json.Unmarshal(raw, &signed); err != nil {
		return Signed{}, false
	}
	return signed, true
}

// Identity is rebuilt on each evaluation rather than cached, because the module
// set changes when somebody plugs a modem in.

// Entitlements evaluates what this installation may do.
//
// machineID is passed in rather than read here: collecting a fingerprint reads
// a dozen sysfs files and this is on the path of every dial, so the caller
// resolves it once and keeps it.
func (s *Store) Entitlements(machineID string, moduleSNs []string, now time.Time) Entitlements {
	if s == nil {
		return Entitlements{granted: map[Scope]map[Feature]bool{}}
	}
	key := strings.Join(append([]string(nil), moduleSNs...), "|")

	s.mu.Lock()
	fresh := !s.cachedAt.IsZero() &&
		now.Sub(s.cachedAt) < cacheTTL &&
		s.modules == key
	if fresh {
		cached := s.cached
		s.mu.Unlock()
		return cached
	}
	s.mu.Unlock()

	evaluated := Evaluate(
		s.Certificates(),
		s.publicKey,
		Identity{MachineID: machineID, ModuleSNs: moduleSNs},
		now,
	)

	s.mu.Lock()
	s.cached = evaluated
	s.cachedAt = now
	s.modules = key
	s.mu.Unlock()
	return evaluated
}

// SaveFingerprint records the identifiers seen at activation.
//
// This is written once and then only read. Rewriting it on every boot would
// destroy the very thing it exists for: a record of what the machine looked
// like WHEN IT WAS ACTIVATED, which is the only way to tell a repaired board
// from a different one later.
func (s *Store) SaveFingerprint(print Fingerprint) error {
	if s == nil || s.root == "" {
		return fmt.Errorf("license: no storage directory is configured")
	}
	path := filepath.Join(s.root, fingerprintFile)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	encoded, err := json.MarshalIndent(print, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}

// StoredFingerprint reads the activation-time fingerprint, if there is one.
func (s *Store) StoredFingerprint() (Fingerprint, bool) {
	if s == nil || s.root == "" {
		return Fingerprint{}, false
	}
	raw, err := os.ReadFile(filepath.Join(s.root, fingerprintFile))
	if err != nil {
		return Fingerprint{}, false
	}
	var print Fingerprint
	if err := json.Unmarshal(raw, &print); err != nil {
		return Fingerprint{}, false
	}
	return print, true
}
