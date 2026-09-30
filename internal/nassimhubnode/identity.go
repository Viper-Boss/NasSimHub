// Package nassimhubnode connects the gateway to NasSimHub Nodes (UFI003 /
// MSM8916 sticks): first-contact discovery, administrator-confirmed pairing,
// key-pinned TLS for every later request, polling, and the projection of each
// Node's SIM and SMS into the gateway's ordinary line and message model.
//
// The Node protocol itself is frozen and vendored under third_party; this
// package only uses it.
package nassimhubnode

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/human-agent65535/nassimhub-node/core/nodeclient"
)

// IdentityFileName holds the gateway's Core signing key. Losing it orphans
// every pairing (each Node would refuse this gateway as "another core"), so it
// lives in the data directory and is included in whatever backs that up.
const IdentityFileName = "nassimhub-core-identity.json"

const coreName = "NasSimHub NAS"

type identityFile struct {
	Version int    `json:"version"`
	CoreID  string `json:"core_id"`
	Name    string `json:"name"`
	Seed    string `json:"seed"`
}

// LoadOrCreateIdentity reads the Core identity from dir, creating it on first
// use. The file is written atomically with mode 0600.
func LoadOrCreateIdentity(dir string) (nodeclient.Identity, error) {
	if dir == "" {
		return nodeclient.Identity{}, errors.New("an identity directory is required")
	}
	path := filepath.Join(dir, IdentityFileName)
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		return decodeIdentity(data)
	case !errors.Is(err, os.ErrNotExist):
		return nodeclient.Identity{}, fmt.Errorf("read core identity: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nodeclient.Identity{}, fmt.Errorf("create identity directory: %w", err)
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nodeclient.Identity{}, fmt.Errorf("generate core identity: %w", err)
	}
	private := ed25519.NewKeyFromSeed(seed)
	record := identityFile{
		Version: 1,
		CoreID:  coreIDFor(private.Public().(ed25519.PublicKey)),
		Name:    coreName,
		Seed:    base64.StdEncoding.EncodeToString(seed),
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return nodeclient.Identity{}, err
	}
	if err := writeFileAtomic(path, encoded, 0o600); err != nil {
		return nodeclient.Identity{}, fmt.Errorf("persist core identity: %w", err)
	}
	return decodeIdentity(encoded)
}

func decodeIdentity(data []byte) (nodeclient.Identity, error) {
	var record identityFile
	if err := json.Unmarshal(data, &record); err != nil {
		return nodeclient.Identity{}, fmt.Errorf("decode core identity: %w", err)
	}
	seed, err := base64.StdEncoding.DecodeString(record.Seed)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nodeclient.Identity{}, errors.New("core identity seed is invalid")
	}
	private := ed25519.NewKeyFromSeed(seed)
	public := private.Public().(ed25519.PublicKey)
	if record.CoreID != coreIDFor(public) {
		return nodeclient.Identity{}, errors.New("core identity id does not match its key")
	}
	name := record.Name
	if name == "" {
		name = coreName
	}
	return nodeclient.Identity{CoreID: record.CoreID, Name: name, PublicKey: public, PrivateKey: private}, nil
}

func coreIDFor(public ed25519.PublicKey) string {
	digest := sha256.Sum256(public)
	return "nas-" + hex.EncodeToString(digest[:6])
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if handle, err := os.Open(dir); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
	return nil
}
