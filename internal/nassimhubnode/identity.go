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
	"github.com/human-agent65535/nassimhub-node/proto"
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
	// PQAlgorithm and PQSeed are the gateway's ADDITIONAL post-quantum signing
	// key. They are absent until a security level that uses one is chosen on a
	// build that has ML-DSA. CoreID derives from the Ed25519 key alone and
	// never changes when they appear. A build that does not know the fields
	// ignores them.
	PQAlgorithm string `json:"pq_algorithm,omitempty"`
	PQSeed      string `json:"pq_seed,omitempty"`
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

// Core post-quantum key states, as reported to the administrator.
const (
	// CorePQActive: the key is loaded and offered to Nodes.
	CorePQActive = "active"
	// CorePQNotOffered: the security level does not use one. Nothing is
	// generated and nothing stored is used.
	CorePQNotOffered = "not_offered"
	// CorePQUnavailable: this build has no ML-DSA.
	CorePQUnavailable = "unavailable"
	// CorePQDamaged: a stored key could not be used. It is not replaced.
	CorePQDamaged = "damaged"
)

// EnsureCorePQ loads the gateway's post-quantum signing key into identity,
// creating it the first time it is needed.
//
// It is called only when the configured security level offers a post-quantum
// identity to Nodes (PQ and PQ_EXTREME). At STANDARD the gateway offers none,
// which is what lets an older gateway build be restored over this one without
// any Node refusing it.
//
// A stored key that cannot be used is never replaced: Nodes that pinned it
// would refuse a new one exactly as they would refuse an impostor, so
// generating one silently would turn a damaged file into a fleet-wide outage
// with no explanation. The caller is told, and the administrator decides.
func EnsureCorePQ(dir string, identity *nodeclient.Identity, algorithm proto.PQSignatureAlgorithm) (string, error) {
	if identity == nil || dir == "" {
		return CorePQDamaged, errors.New("the core identity is required")
	}
	if !proto.StandardPQSupported() {
		return CorePQUnavailable, nil
	}
	if err := proto.ValidatePQSignatureAlgorithm(algorithm); err != nil {
		return CorePQDamaged, err
	}
	path := filepath.Join(dir, IdentityFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return CorePQDamaged, fmt.Errorf("read core identity: %w", err)
	}
	var record identityFile
	if err := json.Unmarshal(data, &record); err != nil {
		return CorePQDamaged, fmt.Errorf("decode core identity: %w", err)
	}
	if record.CoreID != identity.CoreID {
		return CorePQDamaged, errors.New("core identity file does not belong to this identity")
	}
	if record.PQSeed != "" || record.PQAlgorithm != "" {
		seed, err := base64.StdEncoding.DecodeString(record.PQSeed)
		if err != nil {
			return CorePQDamaged, errors.New("stored core post-quantum key is unreadable")
		}
		signer, err := proto.NewMLDSASigner(proto.PQSignatureAlgorithm(record.PQAlgorithm), seed)
		if err != nil {
			return CorePQDamaged, errors.New("stored core post-quantum key is unusable")
		}
		// A stored key of another parameter set is kept: Nodes pinned it.
		identity.PQ = signer
		return CorePQActive, nil
	}
	seed, err := proto.NewMLDSASeed()
	if err != nil {
		return CorePQDamaged, err
	}
	signer, err := proto.NewMLDSASigner(algorithm, seed)
	if err != nil {
		return CorePQDamaged, err
	}
	record.PQAlgorithm = string(algorithm)
	record.PQSeed = base64.StdEncoding.EncodeToString(seed)
	encoded, err := json.Marshal(record)
	if err != nil {
		return CorePQDamaged, err
	}
	if err := writeFileAtomic(path, encoded, 0o600); err != nil {
		return CorePQDamaged, fmt.Errorf("persist core post-quantum key: %w", err)
	}
	identity.PQ = signer
	return CorePQActive, nil
}
