package nassimhubnode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/human-agent65535/nassimhub-node/core/nodeclient"
	"github.com/human-agent65535/nassimhub-node/proto"
)

// Updating a Node's agent from the gateway.
//
// A Node on a USB cable usually has no route to the internet, so the gateway
// carries the update to it. The gateway is a courier: it holds a signed
// manifest and the artifact, hands both to the device over the pairing it
// already has, and the DEVICE decides - against release keys installed on the
// device - whether to accept them. Nothing here can make a device install
// something its keys did not sign.
//
// Three rules shape this file:
//
//   - Nothing is remembered about a device's update state. Every status shown
//     is read from the device at the moment it is asked for; an unreachable
//     device has no status, not the last one. A cached "up to date" is the
//     stale fact an update page must never show.
//   - The gateway's own check of a release is reported for what it is. With
//     release keys configured it verifies the manifest and refuses a bad one
//     before carrying it. Without them it says the release is unverified HERE;
//     it does not present the absence of a check as a pass.
//   - Applying is a separate, explicit request for a named release, and the
//     update is only confirmed after the device has come back, re-proved its
//     identity and reports the version the manifest promised. A device that
//     does not come back is left alone: it rolls itself back when its
//     confirmation window closes.

var (
	// ErrNodeUpdatesUnsupported reports a Node whose agent has no update
	// endpoints. It is not a failure of the update.
	ErrNodeUpdatesUnsupported = errors.New("node does not support agent updates")
	// ErrNodeUpdateBusy reports a second update operation on a Node that
	// already has one in progress from this gateway.
	ErrNodeUpdateBusy = errors.New("another update operation is in progress for this node")
	// ErrNodeReleaseUnknown reports a release that was not imported.
	ErrNodeReleaseUnknown = errors.New("release is not in the gateway's store")
	// ErrNodeReleaseIncomplete reports a release whose artifact has not been
	// imported yet.
	ErrNodeReleaseIncomplete = errors.New("release artifact has not been imported")
	// ErrNodeReleaseRejected reports a release the gateway or the device
	// refused; the wrapped message says why.
	ErrNodeReleaseRejected = errors.New("release rejected")
	// ErrNodeUpdateNotConfirmed reports an applied update that did not prove
	// itself in time. The device rolls back on its own.
	ErrNodeUpdateNotConfirmed = errors.New("the node did not come back on the new version in time")
)

// GatewayVerification says what the gateway itself established about a release.
type GatewayVerification string

const (
	// GatewayVerified: the manifest verified against the gateway's release
	// keys.
	GatewayVerified GatewayVerification = "verified"
	// GatewayUnverified: the gateway has no release keys, so it checked
	// nothing. The device still verifies.
	GatewayUnverified GatewayVerification = "unverified"
)

// NodeRelease describes one imported release.
type NodeRelease struct {
	ReleaseID      string    `json:"release_id"`
	Version        string    `json:"version"`
	Channel        string    `json:"channel"`
	Platform       string    `json:"platform"`
	Arch           string    `json:"arch"`
	Size           int64     `json:"size"`
	SHA256         string    `json:"sha256"`
	ReleasedAt     time.Time `json:"released_at,omitzero"`
	MinCoreVersion string    `json:"min_core_version,omitempty"`
	Notes          string    `json:"notes,omitempty"`
	// KeyID and PQKeyID name the keys the manifest claims; PQAlgorithm the
	// post-quantum algorithm, empty for a classical-only manifest.
	KeyID       string `json:"key_id"`
	PQKeyID     string `json:"pq_key_id,omitempty"`
	PQAlgorithm string `json:"pq_algorithm,omitempty"`
	// Gateway is what THIS gateway verified; Signatures the outcome when it
	// did ("classical" or "hybrid").
	Gateway    GatewayVerification `json:"gateway_verification"`
	Signatures string              `json:"signatures,omitempty"`
	// ArtifactReady is true once the artifact is stored and matches the
	// manifest's size and hash.
	ArtifactReady bool      `json:"artifact_ready"`
	ImportedAt    time.Time `json:"imported_at,omitzero"`
	// Source says how the manifest reached this gateway: empty for one an
	// administrator uploaded, NodeReleaseSourcePeer for one fetched from
	// another gateway. It is a label for the page, never a reason to trust or
	// distrust the release - the signature decides that either way.
	Source string `json:"source,omitempty"`
}

// NodeReleaseSourcePeer marks a release another gateway handed over.
const NodeReleaseSourcePeer = "peer"

// NodeReleaseStoreOptions configures the store.
type NodeReleaseStoreOptions struct {
	// Dir is where releases are kept.
	Dir string
	// Keyring and PQKeyring are the gateway's release keys. Both empty means
	// the gateway verifies nothing itself and says so.
	Keyring   proto.OTAKeyring
	PQKeyring proto.OTAPQKeyring
	// SignaturePolicy applies when keys are configured.
	SignaturePolicy proto.OTASignaturePolicy
	// MaxReleases bounds how many releases are kept; the default is 8.
	MaxReleases int
	Now         func() time.Time
}

// NodeReleaseStore keeps agent releases the gateway can carry to Nodes.
type NodeReleaseStore struct {
	dir     string
	keys    proto.OTAKeyring
	pqKeys  proto.OTAPQKeyring
	policy  proto.OTASignaturePolicy
	maximum int
	now     func() time.Time
	mu      sync.Mutex
}

const (
	releaseManifestName = "manifest.json"
	releaseArtifactName = "artifact"
	releaseMetaName     = "imported.json"
)

// OpenNodeReleaseStore opens (creating if needed) the release directory.
func OpenNodeReleaseStore(options NodeReleaseStoreOptions) (*NodeReleaseStore, error) {
	if strings.TrimSpace(options.Dir) == "" {
		return nil, errors.New("node release store requires a directory")
	}
	if err := os.MkdirAll(options.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("node release store: %w", err)
	}
	policy := options.SignaturePolicy
	if policy == "" {
		policy = proto.OTASignaturePreferred
	}
	if err := proto.ValidateOTASignaturePolicy(policy); err != nil {
		return nil, err
	}
	maximum := options.MaxReleases
	if maximum <= 0 {
		maximum = 8
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &NodeReleaseStore{dir: options.Dir, keys: options.Keyring, pqKeys: options.PQKeyring,
		policy: policy, maximum: maximum, now: now}, nil
}

// VerifiesReleases reports whether the gateway has release keys of its own.
func (s *NodeReleaseStore) VerifiesReleases() bool { return len(s.keys) > 0 }

func agentArtifactOf(manifest proto.OTAManifest) (proto.OTAArtifact, error) {
	for _, artifact := range manifest.Artifacts {
		if artifact.Kind == proto.OTAArtifactAgent {
			return artifact, nil
		}
	}
	return proto.OTAArtifact{}, errors.New("manifest carries no agent artifact")
}

// decode parses a signed manifest and, when the gateway has keys, verifies it.
func (s *NodeReleaseStore) decode(raw []byte) (proto.OTASignedManifest, NodeRelease, error) {
	if len(raw) == 0 || len(raw) > proto.OTAMaxManifestBytes {
		return proto.OTASignedManifest{}, NodeRelease{}, fmt.Errorf("%w: the manifest is empty or larger than %d bytes",
			ErrNodeReleaseRejected, proto.OTAMaxManifestBytes)
	}
	var signed proto.OTASignedManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&signed); err != nil || len(signed.Manifest) == 0 {
		return proto.OTASignedManifest{}, NodeRelease{}, fmt.Errorf("%w: not a signed manifest", ErrNodeReleaseRejected)
	}
	release := NodeRelease{KeyID: signed.KeyID, PQKeyID: signed.PQKeyID, PQAlgorithm: string(signed.PQAlgorithm),
		Gateway: GatewayUnverified}
	var manifest proto.OTAManifest
	if s.VerifiesReleases() {
		verified, outcome, err := proto.VerifyManifestHybrid(signed, s.keys, s.pqKeys, s.policy)
		if err != nil {
			return proto.OTASignedManifest{}, NodeRelease{}, fmt.Errorf("%w: %v", ErrNodeReleaseRejected, err)
		}
		manifest = verified
		release.Gateway = GatewayVerified
		release.Signatures = string(outcome)
	} else if err := json.Unmarshal(signed.Manifest, &manifest); err != nil {
		return proto.OTASignedManifest{}, NodeRelease{}, fmt.Errorf("%w: the manifest is not readable", ErrNodeReleaseRejected)
	}
	if err := proto.ValidateOTAReleaseID(manifest.ReleaseID); err != nil {
		return proto.OTASignedManifest{}, NodeRelease{}, fmt.Errorf("%w: %v", ErrNodeReleaseRejected, err)
	}
	if manifest.SchemaVersion != proto.OTASchemaVersion || manifest.ProtocolMajor != proto.ProtocolMajor {
		return proto.OTASignedManifest{}, NodeRelease{}, fmt.Errorf("%w: the release is for another schema or protocol generation", ErrNodeReleaseRejected)
	}
	if _, err := proto.ParseVersion(manifest.Version); err != nil {
		return proto.OTASignedManifest{}, NodeRelease{}, fmt.Errorf("%w: the release version is not readable", ErrNodeReleaseRejected)
	}
	artifact, err := agentArtifactOf(manifest)
	if err != nil {
		return proto.OTASignedManifest{}, NodeRelease{}, fmt.Errorf("%w: %v", ErrNodeReleaseRejected, err)
	}
	digest, err := hex.DecodeString(artifact.SHA256)
	if err != nil || len(digest) != sha256.Size || artifact.Size <= 0 || artifact.Size > proto.OTAMaxArtifactBytes {
		return proto.OTASignedManifest{}, NodeRelease{}, fmt.Errorf("%w: the agent artifact's size or hash is not acceptable", ErrNodeReleaseRejected)
	}
	release.ReleaseID = manifest.ReleaseID
	release.Version = manifest.Version
	release.Channel = string(manifest.Channel)
	release.Platform = string(manifest.Platform)
	release.Arch = artifact.Arch
	release.Size = artifact.Size
	release.SHA256 = strings.ToLower(artifact.SHA256)
	release.ReleasedAt = manifest.ReleasedAt
	release.MinCoreVersion = manifest.MinCoreVersion
	release.Notes = manifest.Notes
	return signed, release, nil
}

type releaseMeta struct {
	ImportedAt time.Time `json:"imported_at"`
	Source     string    `json:"source,omitempty"`
}

// ImportManifest stores a signed manifest. The bytes are kept exactly as
// given: the signature covers them, and the device must receive what the
// publisher signed.
func (s *NodeReleaseStore) ImportManifest(raw []byte) (NodeRelease, error) {
	return s.ImportManifestFrom(raw, "")
}

// ImportManifestFrom is ImportManifest with a source label recorded beside
// the manifest. Every check is the same: the label changes what the page
// says about where a release came from and nothing about whether it is kept.
// A manifest that is already stored keeps the label it was first given.
func (s *NodeReleaseStore) ImportManifestFrom(raw []byte, source string) (NodeRelease, error) {
	if source != "" && source != NodeReleaseSourcePeer {
		return NodeRelease{}, fmt.Errorf("%w: unknown release source %q", ErrNodeReleaseRejected, source)
	}
	_, release, err := s.decode(raw)
	if err != nil {
		return NodeRelease{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	directory := filepath.Join(s.dir, release.ReleaseID)
	if existing, readErr := os.ReadFile(filepath.Join(directory, releaseManifestName)); readErr == nil {
		if !bytes.Equal(existing, raw) {
			// One release id, two different manifests: never silently replace
			// what an administrator already reviewed.
			return NodeRelease{}, fmt.Errorf("%w: a different manifest is already stored as %s; remove it first",
				ErrNodeReleaseRejected, release.ReleaseID)
		}
		return s.describeLocked(release.ReleaseID)
	}
	if ids, _ := s.idsLocked(); len(ids) >= s.maximum {
		return NodeRelease{}, fmt.Errorf("%w: the store already holds %d releases; remove one first",
			ErrNodeReleaseRejected, len(ids))
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return NodeRelease{}, err
	}
	meta, _ := json.Marshal(releaseMeta{ImportedAt: s.now().UTC(), Source: source})
	if err := writeFileAtomic(filepath.Join(directory, releaseMetaName), meta, 0o600); err != nil {
		return NodeRelease{}, err
	}
	if err := writeFileAtomic(filepath.Join(directory, releaseManifestName), raw, 0o600); err != nil {
		return NodeRelease{}, err
	}
	return s.describeLocked(release.ReleaseID)
}

// ImportArtifact stores the artifact of an imported manifest. It reads at most
// the size the manifest declares plus one byte, and keeps the result only if
// both the size and the SHA-256 match.
func (s *NodeReleaseStore) ImportArtifact(releaseID string, body io.Reader) (NodeRelease, error) {
	if err := proto.ValidateOTAReleaseID(releaseID); err != nil {
		return NodeRelease{}, ErrNodeReleaseUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := s.describeLocked(releaseID)
	if err != nil {
		return NodeRelease{}, err
	}
	if release.ArtifactReady {
		return release, nil
	}
	directory := filepath.Join(s.dir, releaseID)
	temporary, err := os.CreateTemp(directory, ".artifact-*")
	if err != nil {
		return NodeRelease{}, err
	}
	name := temporary.Name()
	defer os.Remove(name)
	digest := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, digest), io.LimitReader(body, release.Size+1))
	if copyErr == nil {
		copyErr = temporary.Sync()
	}
	if closeErr := temporary.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return NodeRelease{}, copyErr
	}
	if written != release.Size {
		return NodeRelease{}, fmt.Errorf("%w: the artifact is not the %d bytes the manifest declares",
			ErrNodeReleaseRejected, release.Size)
	}
	if hex.EncodeToString(digest.Sum(nil)) != release.SHA256 {
		return NodeRelease{}, fmt.Errorf("%w: the artifact does not match the manifest's hash", ErrNodeReleaseRejected)
	}
	if err := os.Rename(name, filepath.Join(directory, releaseArtifactName)); err != nil {
		return NodeRelease{}, err
	}
	return s.describeLocked(releaseID)
}

func (s *NodeReleaseStore) idsLocked() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, entry := range entries {
		if entry.IsDir() && proto.ValidateOTAReleaseID(entry.Name()) == nil {
			if _, err := os.Stat(filepath.Join(s.dir, entry.Name(), releaseManifestName)); err == nil {
				ids = append(ids, entry.Name())
			}
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// describeLocked re-reads and re-checks a release from disk. Nothing about a
// release is cached: the manifest is decoded (and verified, with keys) and the
// artifact's presence and size are looked at each time.
func (s *NodeReleaseStore) describeLocked(releaseID string) (NodeRelease, error) {
	directory := filepath.Join(s.dir, releaseID)
	raw, err := os.ReadFile(filepath.Join(directory, releaseManifestName))
	if err != nil {
		return NodeRelease{}, ErrNodeReleaseUnknown
	}
	_, release, err := s.decode(raw)
	if err != nil {
		return NodeRelease{}, err
	}
	if release.ReleaseID != releaseID {
		return NodeRelease{}, fmt.Errorf("%w: directory %s holds the manifest of another release", ErrNodeReleaseRejected, releaseID)
	}
	if info, err := os.Stat(filepath.Join(directory, releaseArtifactName)); err == nil && info.Mode().IsRegular() && info.Size() == release.Size {
		release.ArtifactReady = true
	}
	var meta releaseMeta
	if content, err := os.ReadFile(filepath.Join(directory, releaseMetaName)); err == nil && json.Unmarshal(content, &meta) == nil {
		release.ImportedAt = meta.ImportedAt
		if meta.Source == NodeReleaseSourcePeer {
			release.Source = meta.Source
		}
	}
	return release, nil
}

// Release describes one stored release.
func (s *NodeReleaseStore) Release(releaseID string) (NodeRelease, error) {
	if err := proto.ValidateOTAReleaseID(releaseID); err != nil {
		return NodeRelease{}, ErrNodeReleaseUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.describeLocked(releaseID)
}

// Releases lists the stored releases, newest version first. A release that no
// longer passes the gateway's checks (a key was withdrawn, a file was damaged)
// is left out rather than offered.
func (s *NodeReleaseStore) Releases() []NodeRelease {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, _ := s.idsLocked()
	releases := make([]NodeRelease, 0, len(ids))
	for _, id := range ids {
		if release, err := s.describeLocked(id); err == nil {
			releases = append(releases, release)
		}
	}
	sort.SliceStable(releases, func(i, j int) bool {
		left, leftErr := proto.ParseVersion(releases[i].Version)
		right, rightErr := proto.ParseVersion(releases[j].Version)
		if leftErr != nil || rightErr != nil {
			return releases[i].ReleaseID > releases[j].ReleaseID
		}
		return left.Compare(right) > 0
	})
	return releases
}

// Remove deletes a stored release.
func (s *NodeReleaseStore) Remove(releaseID string) error {
	if err := proto.ValidateOTAReleaseID(releaseID); err != nil {
		return ErrNodeReleaseUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	directory := filepath.Join(s.dir, releaseID)
	if _, err := os.Stat(directory); err != nil {
		return ErrNodeReleaseUnknown
	}
	return os.RemoveAll(directory)
}

// open returns the exact signed manifest and the artifact, re-hashed.
func (s *NodeReleaseStore) open(releaseID string) (proto.OTASignedManifest, NodeRelease, *os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := s.describeLocked(releaseID)
	if err != nil {
		return proto.OTASignedManifest{}, NodeRelease{}, nil, err
	}
	if !release.ArtifactReady {
		return proto.OTASignedManifest{}, NodeRelease{}, nil, ErrNodeReleaseIncomplete
	}
	raw, err := os.ReadFile(filepath.Join(s.dir, releaseID, releaseManifestName))
	if err != nil {
		return proto.OTASignedManifest{}, NodeRelease{}, nil, ErrNodeReleaseUnknown
	}
	signed, _, err := s.decode(raw)
	if err != nil {
		return proto.OTASignedManifest{}, NodeRelease{}, nil, err
	}
	file, err := os.Open(filepath.Join(s.dir, releaseID, releaseArtifactName))
	if err != nil {
		return proto.OTASignedManifest{}, NodeRelease{}, nil, ErrNodeReleaseIncomplete
	}
	// The file sat on disk since it was imported. Hash it again before it is
	// carried anywhere; the device would refuse a damaged one, but only after
	// the whole transfer.
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil || hex.EncodeToString(digest.Sum(nil)) != release.SHA256 {
		file.Close()
		return proto.OTASignedManifest{}, NodeRelease{}, nil,
			fmt.Errorf("%w: the stored artifact no longer matches its manifest", ErrNodeReleaseRejected)
	}
	return signed, release, file, nil
}

// The four accessors below exist for the peer exchange of Node releases
// (internal/nodereleasep2p). They add no way in: everything that enters the
// store still goes through ImportManifest and ImportArtifact above.

// VerificationKeys returns the release keys and signature policy this store
// verifies with, so a manifest offered by another gateway is judged by exactly
// the keys an uploaded one is. The maps are copies.
func (s *NodeReleaseStore) VerificationKeys() (proto.OTAKeyring, proto.OTAPQKeyring, proto.OTASignaturePolicy) {
	classical := make(proto.OTAKeyring, len(s.keys))
	for id, key := range s.keys {
		classical[id] = key
	}
	postQuantum := make(proto.OTAPQKeyring, len(s.pqKeys))
	for id, key := range s.pqKeys {
		postQuantum[id] = key
	}
	return classical, postQuantum, s.policy
}

// Capacity reports how many releases are stored and how many the store keeps.
func (s *NodeReleaseStore) Capacity() (stored, maximum int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, _ := s.idsLocked()
	return len(ids), s.maximum
}

// SignedManifest returns a stored manifest byte for byte as it was imported,
// with what the store establishes about it now.
func (s *NodeReleaseStore) SignedManifest(releaseID string) ([]byte, NodeRelease, error) {
	if err := proto.ValidateOTAReleaseID(releaseID); err != nil {
		return nil, NodeRelease{}, ErrNodeReleaseUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := s.describeLocked(releaseID)
	if err != nil {
		return nil, NodeRelease{}, err
	}
	raw, err := os.ReadFile(filepath.Join(s.dir, releaseID, releaseManifestName))
	if err != nil {
		return nil, NodeRelease{}, ErrNodeReleaseUnknown
	}
	return raw, release, nil
}

// OpenArtifact opens a stored artifact for reading after hashing it again
// against its manifest, exactly as it is before being carried to a device.
// The caller closes the file.
func (s *NodeReleaseStore) OpenArtifact(releaseID string) (*os.File, NodeRelease, error) {
	if err := proto.ValidateOTAReleaseID(releaseID); err != nil {
		return nil, NodeRelease{}, ErrNodeReleaseUnknown
	}
	_, release, file, err := s.open(releaseID)
	if err != nil {
		return nil, NodeRelease{}, err
	}
	return file, release, nil
}

// ---------------------------------------------------------------------------
// Fleet operations
// ---------------------------------------------------------------------------

// NodeUpdateView is one Node's update state, read from the device just now.
type NodeUpdateView struct {
	DeviceID string `json:"node_id"`
	// Reachable is false when the device could not be asked. Status is then
	// absent - not the last known one.
	Reachable bool `json:"reachable"`
	// Supported is false for an agent without update endpoints, or one that
	// reports it cannot install (Status.UnsupportedReason says why).
	Supported bool                   `json:"supported"`
	Reason    string                 `json:"reason,omitempty"`
	Status    *proto.OTADeviceStatus `json:"status,omitempty"`
	// ReadAt is when the device answered.
	ReadAt time.Time `json:"read_at,omitzero"`
}

func (f *Fleet) updateClient(deviceID string) (*liveNode, *nodeclient.Client, error) {
	live := f.lookup(deviceID)
	if live == nil {
		return nil, nil, ErrUnknownNode
	}
	live.mu.Lock()
	state := live.state
	live.mu.Unlock()
	if state.Revoked {
		return nil, nil, ErrNodeUnavailable
	}
	client, _ := live.handles()
	if client == nil {
		return nil, nil, ErrNodeUnavailable
	}
	return live, client, nil
}

func mapUpdateError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, nodeclient.ErrUpdatesUnsupported):
		return ErrNodeUpdatesUnsupported
	}
	if operation, ok := proto.AsOperationError(err); ok {
		switch operation.Code {
		case proto.ErrorInvalidArgument, proto.ErrorConflict, proto.ErrorNotSupported, proto.ErrorFailedPrecondition,
			proto.ErrorPermissionDenied:
			return fmt.Errorf("%w: %s", ErrNodeReleaseRejected, operation.Message)
		}
	}
	return err
}

// NodeUpdateStatus asks one Node where its update machine is.
func (f *Fleet) NodeUpdateStatus(ctx context.Context, deviceID string) (NodeUpdateView, error) {
	_, client, err := f.updateClient(deviceID)
	if errors.Is(err, ErrUnknownNode) {
		return NodeUpdateView{}, err
	}
	view := NodeUpdateView{DeviceID: strings.TrimSpace(deviceID)}
	if err != nil {
		view.Reason = "the device is not available"
		return view, nil
	}
	status, err := client.UpdateStatus(ctx)
	if err != nil {
		if errors.Is(err, nodeclient.ErrUpdatesUnsupported) {
			view.Reachable = true
			view.Reason = "this device's agent has no update support"
			view.ReadAt = f.now().UTC()
			return view, nil
		}
		view.Reason = "the device did not answer"
		return view, nil
	}
	view.Reachable = true
	view.Supported = status.Supported
	view.Reason = status.UnsupportedReason
	view.Status = &status
	view.ReadAt = f.now().UTC()
	return view, nil
}

func (f *Fleet) lockUpdate(deviceID string) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updating == nil {
		f.updating = map[string]struct{}{}
	}
	key := strings.TrimSpace(deviceID)
	if _, busy := f.updating[key]; busy {
		return nil, ErrNodeUpdateBusy
	}
	f.updating[key] = struct{}{}
	return func() {
		f.mu.Lock()
		delete(f.updating, key)
		f.mu.Unlock()
	}, nil
}

// uploadAttempts bounds how often an interrupted transfer is resumed within
// one push. Each resume starts from the offset the DEVICE reports.
const uploadAttempts = 4

// PushNodeUpdate offers a stored release to a Node and transfers the artifact,
// resuming from whatever the device already holds. It does not apply it.
func (f *Fleet) PushNodeUpdate(ctx context.Context, store *NodeReleaseStore, deviceID, releaseID string) (NodeUpdateView, error) {
	if store == nil {
		return NodeUpdateView{}, ErrNodeUpdatesUnsupported
	}
	_, client, err := f.updateClient(deviceID)
	if err != nil {
		return NodeUpdateView{}, err
	}
	unlock, err := f.lockUpdate(deviceID)
	if err != nil {
		return NodeUpdateView{}, err
	}
	defer unlock()
	signed, release, artifact, err := store.open(releaseID)
	if err != nil {
		return NodeUpdateView{}, err
	}
	defer artifact.Close()

	offer, err := client.OfferUpdate(ctx, signed)
	if err != nil {
		return NodeUpdateView{}, mapUpdateError(err)
	}
	if offer.Manifest.ReleaseID != release.ReleaseID {
		return NodeUpdateView{}, fmt.Errorf("%w: the device answered for another release", ErrNodeReleaseRejected)
	}
	status := offer.Status
	offset := status.ReceivedBytes
	var lastErr error
	for attempt := 0; attempt < uploadAttempts && offset < release.Size; attempt++ {
		if offset < 0 || offset > release.Size {
			return NodeUpdateView{}, fmt.Errorf("%w: the device reports an impossible transfer offset", ErrNodeReleaseRejected)
		}
		if _, err := artifact.Seek(offset, io.SeekStart); err != nil {
			return NodeUpdateView{}, err
		}
		next, err := client.UploadUpdate(ctx, release.ReleaseID, offset, artifact, release.Size-offset)
		if err == nil {
			status, offset, lastErr = next, next.ReceivedBytes, nil
			if next.State != proto.OTADownloading {
				break
			}
			continue
		}
		lastErr = err
		var mismatch *nodeclient.ErrUploadOffset
		if errors.As(err, &mismatch) {
			offset = mismatch.Offset
			continue
		}
		if ctx.Err() != nil {
			return NodeUpdateView{}, ctx.Err()
		}
		if _, refused := proto.AsOperationError(err); refused {
			return NodeUpdateView{}, mapUpdateError(err)
		}
		// A transport failure. Ask the device where it is and resume there.
		current, statusErr := client.UpdateStatus(ctx)
		if statusErr != nil {
			return NodeUpdateView{}, fmt.Errorf("%w: the transfer was interrupted and the device did not answer", ErrNodeUnavailable)
		}
		if current.ReleaseID != release.ReleaseID {
			return NodeUpdateView{}, fmt.Errorf("%w: the device dropped the offered release", ErrNodeReleaseRejected)
		}
		status, offset = current, current.ReceivedBytes
	}
	if lastErr != nil {
		return NodeUpdateView{}, mapUpdateError(lastErr)
	}
	// Report what the device says now, not what the last upload returned.
	if current, err := client.UpdateStatus(ctx); err == nil {
		status = current
	}
	if status.State == proto.OTAFailed {
		return NodeUpdateView{}, fmt.Errorf("%w: %s", ErrNodeReleaseRejected, status.Error)
	}
	return NodeUpdateView{DeviceID: strings.TrimSpace(deviceID), Reachable: true, Supported: status.Supported,
		Status: &status, ReadAt: f.now().UTC()}, nil
}

// NodeUpdateApplyOptions tunes ApplyNodeUpdate.
type NodeUpdateApplyOptions struct {
	// ReturnWithin bounds how long the gateway waits for the device to come
	// back on the new version before giving up on confirming. The default is
	// 90 seconds. The device's own confirmation window is longer and is what
	// actually triggers the rollback.
	ReturnWithin time.Duration
	// PollEvery is how often the device is asked while waiting.
	PollEvery time.Duration
}

// ApplyNodeUpdate switches a Node to a release it has validated, waits for it
// to come back, and confirms the update only when the device (1) answers on an
// authenticated session, (2) reports the applied release as pending
// confirmation on the promised version, and (3) passes the gateway's ordinary
// identity and compatibility checks again. Otherwise it confirms nothing and
// the device rolls itself back.
func (f *Fleet) ApplyNodeUpdate(ctx context.Context, deviceID, releaseID string, options NodeUpdateApplyOptions) (NodeUpdateView, error) {
	if err := proto.ValidateOTAReleaseID(releaseID); err != nil {
		return NodeUpdateView{}, ErrNodeReleaseUnknown
	}
	_, client, err := f.updateClient(deviceID)
	if err != nil {
		return NodeUpdateView{}, err
	}
	unlock, err := f.lockUpdate(deviceID)
	if err != nil {
		return NodeUpdateView{}, err
	}
	defer unlock()
	before, err := client.UpdateStatus(ctx)
	if err != nil {
		return NodeUpdateView{}, mapUpdateError(err)
	}
	if before.ReleaseID != releaseID || before.State != proto.OTAReady {
		return NodeUpdateView{}, fmt.Errorf("%w: the device has not validated release %s (state %s)",
			ErrNodeReleaseRejected, releaseID, before.State)
	}
	target := before.TargetVersion
	if _, err := client.ApplyUpdate(ctx, releaseID); err != nil {
		return NodeUpdateView{}, mapUpdateError(err)
	}

	wait := options.ReturnWithin
	if wait <= 0 {
		wait = 90 * time.Second
	}
	every := options.PollEvery
	if every <= 0 {
		every = time.Second
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	var last *proto.OTADeviceStatus
	for {
		select {
		case <-ctx.Done():
			return NodeUpdateView{}, ctx.Err()
		case <-deadline.C:
			view := NodeUpdateView{DeviceID: strings.TrimSpace(deviceID), Status: last, Reachable: last != nil}
			if last != nil {
				view.Supported, view.ReadAt = last.Supported, f.now().UTC()
			}
			return view, ErrNodeUpdateNotConfirmed
		case <-ticker.C:
		}
		status, err := client.UpdateStatus(ctx)
		if err != nil {
			continue // restarting
		}
		last = &status
		switch {
		case status.ReleaseID == releaseID && status.State == proto.OTAPendingConfirm && !status.RestartPending:
			if status.ActiveRelease != releaseID || (target != "" && status.CurrentVersion != target) {
				continue
			}
			// The device is back on the new agent. Hold it to the same checks
			// as any other poll before telling it to keep the update.
			node, refreshErr := f.Refresh(ctx, deviceID)
			if refreshErr != nil || !node.Online || !node.Compatible || node.IdentityRefused {
				continue
			}
			confirmed, confirmErr := client.ConfirmUpdate(ctx, releaseID)
			if confirmErr != nil {
				continue
			}
			return NodeUpdateView{DeviceID: strings.TrimSpace(deviceID), Reachable: true, Supported: confirmed.Supported,
				Status: &confirmed, ReadAt: f.now().UTC()}, nil
		case status.State == proto.OTAFailed || (status.State == proto.OTAIdle && status.ReleaseID != releaseID && !status.RestartPending && status.ActiveRelease != releaseID && status.RestartReason != ""):
			// The device undid the update on its own.
			return NodeUpdateView{DeviceID: strings.TrimSpace(deviceID), Reachable: true, Supported: status.Supported,
					Status: &status, ReadAt: f.now().UTC()},
				fmt.Errorf("%w: %s", ErrNodeUpdateNotConfirmed, firstNonEmpty(status.Error, string(status.RestartReason)))
		}
	}
}

// ConfirmNodeUpdate confirms an applied update by hand - for an administrator
// who checked the device after the gateway's own wait ran out.
func (f *Fleet) ConfirmNodeUpdate(ctx context.Context, deviceID, releaseID string) (NodeUpdateView, error) {
	return f.updateAction(ctx, deviceID, releaseID, (*nodeclient.Client).ConfirmUpdate)
}

// RollbackNodeUpdate undoes an applied, unconfirmed update.
func (f *Fleet) RollbackNodeUpdate(ctx context.Context, deviceID, releaseID string) (NodeUpdateView, error) {
	return f.updateAction(ctx, deviceID, releaseID, (*nodeclient.Client).RollbackUpdate)
}

func (f *Fleet) updateAction(
	ctx context.Context, deviceID, releaseID string,
	action func(*nodeclient.Client, context.Context, string) (proto.OTADeviceStatus, error),
) (NodeUpdateView, error) {
	if err := proto.ValidateOTAReleaseID(releaseID); err != nil {
		return NodeUpdateView{}, ErrNodeReleaseUnknown
	}
	_, client, err := f.updateClient(deviceID)
	if err != nil {
		return NodeUpdateView{}, err
	}
	unlock, err := f.lockUpdate(deviceID)
	if err != nil {
		return NodeUpdateView{}, err
	}
	defer unlock()
	status, err := action(client, ctx, releaseID)
	if err != nil {
		return NodeUpdateView{}, mapUpdateError(err)
	}
	return NodeUpdateView{DeviceID: strings.TrimSpace(deviceID), Reachable: true, Supported: status.Supported,
		Status: &status, ReadAt: f.now().UTC()}, nil
}

// ---------------------------------------------------------------------------
// The fleet's own release store
// ---------------------------------------------------------------------------

// NodeReleaseCatalog is what the update page lists.
type NodeReleaseCatalog struct {
	Releases []NodeRelease `json:"releases"`
	// GatewayVerifies is whether this gateway has release keys. When false
	// every release is listed as unverified here; devices still verify.
	GatewayVerifies bool `json:"gateway_verifies"`
	// Available is false when the gateway has no release store at all.
	Available bool `json:"available"`
}

// SetReleaseStore attaches the store the fleet's update operations use.
func (f *Fleet) SetReleaseStore(store *NodeReleaseStore) {
	f.mu.Lock()
	f.releases = store
	f.mu.Unlock()
}

func (f *Fleet) releaseStore() *NodeReleaseStore {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.releases
}

// NodeReleases lists the stored releases, read from disk now.
func (f *Fleet) NodeReleases() NodeReleaseCatalog {
	store := f.releaseStore()
	if store == nil {
		return NodeReleaseCatalog{Releases: []NodeRelease{}}
	}
	return NodeReleaseCatalog{Releases: store.Releases(), GatewayVerifies: store.VerifiesReleases(), Available: true}
}

// ImportNodeReleaseManifest stores a signed manifest.
func (f *Fleet) ImportNodeReleaseManifest(raw []byte) (NodeRelease, error) {
	store := f.releaseStore()
	if store == nil {
		return NodeRelease{}, ErrNodeUpdatesUnsupported
	}
	return store.ImportManifest(raw)
}

// ImportNodeReleaseArtifact stores the artifact of an imported manifest.
func (f *Fleet) ImportNodeReleaseArtifact(releaseID string, body io.Reader) (NodeRelease, error) {
	store := f.releaseStore()
	if store == nil {
		return NodeRelease{}, ErrNodeUpdatesUnsupported
	}
	return store.ImportArtifact(releaseID, body)
}

// RemoveNodeRelease deletes a stored release. Devices that already hold it are
// not affected.
func (f *Fleet) RemoveNodeRelease(releaseID string) error {
	store := f.releaseStore()
	if store == nil {
		return ErrNodeUpdatesUnsupported
	}
	return store.Remove(releaseID)
}

// PushStoredNodeUpdate is PushNodeUpdate from the fleet's own store.
func (f *Fleet) PushStoredNodeUpdate(ctx context.Context, deviceID, releaseID string) (NodeUpdateView, error) {
	return f.PushNodeUpdate(ctx, f.releaseStore(), deviceID, releaseID)
}
