package nodereleasep2p

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/human-agent65535/nassimhub-node/proto"
)

// Admission: what has to be true before one payload byte is asked for.
//
// An administrator who uploads a release chose it. Here an anonymous gateway
// is choosing, so nothing is fetched on its say-so: the manifest it shows must
// be the publisher's (signature), must be for a device this gateway actually
// has (platform, architecture), and must be worth having (newer than what is
// stored, not older than what is running). Only then is the cache asked for
// room. The order is the brief's and is fixed by admissionOrder below; the
// cheap and the unforgeable come first, so that a forged offer costs one
// signature check and never reaches a question about disk.

// Reason names why an offer, a table or a payload was refused. Each is counted
// separately in the status document.
type Reason string

const (
	ReasonManifestSize  Reason = "manifest_size"
	ReasonSignature     Reason = "signature"
	ReasonSchema        Reason = "schema"
	ReasonPlatform      Reason = "platform"
	ReasonChannel       Reason = "channel"
	ReasonDowngrade     Reason = "downgrade"
	ReasonArtifact      Reason = "artifact"
	ReasonStoreFull     Reason = "store_full"
	ReasonBusy          Reason = "busy"
	ReasonCacheFull     Reason = "cache_full"
	ReasonMixed         Reason = "mixed"
	ReasonTableMismatch Reason = "table_mismatch"
	ReasonChunkMismatch Reason = "chunk_mismatch"
	ReasonHashMismatch  Reason = "hash_mismatch"
)

// penalty is what a refusal costs the peer that caused it.
type penalty int

const (
	// penaltyNone: the refusal is about this gateway (no room, busy), not
	// about anything the peer did.
	penaltyNone penalty = iota
	// penaltyPolicy: a genuine release this gateway does not want. The peer
	// is not asked again for a while; an honest gateway that simply holds an
	// older release ends up here.
	penaltyPolicy
	// penaltyForged: something the publisher did not sign, or bytes that do
	// not match what was promised. Backed off for much longer, and longer
	// again each time.
	penaltyForged
)

type refusal struct {
	reason  Reason
	detail  string
	penalty penalty
}

func refuse(reason Reason, cost penalty, format string, arguments ...any) *refusal {
	return &refusal{reason: reason, penalty: cost, detail: fmt.Sprintf(format, arguments...)}
}

// Target is one kind of device a release may be for. An empty Arch accepts
// any architecture.
type Target struct {
	Platform string
	Arch     string
}

func (target Target) String() string {
	if target.Arch == "" {
		return target.Platform
	}
	return target.Platform + "/" + target.Arch
}

// platformArch is the architecture each known hardware platform runs. A
// platform that is not listed (the software-only development Node) is matched
// on its name alone.
var platformArch = map[string]string{
	string(proto.PlatformMSM8916): "arm64",
}

var targetPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}(/[a-z0-9][a-z0-9_-]{0,31})?$`)

// ParseTargets reads an operator's allowlist: "platform" or "platform/arch",
// separated by commas or whitespace. Entries that do not parse are returned
// separately so the caller can log them rather than silently widen or narrow
// what is accepted.
func ParseTargets(value string) (kept []Target, dropped []string) {
	for _, field := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	}) {
		field = strings.ToLower(field)
		if !targetPattern.MatchString(field) {
			dropped = append(dropped, field)
			continue
		}
		platform, arch, _ := strings.Cut(field, "/")
		kept = append(kept, Target{Platform: platform, Arch: arch})
	}
	return kept, dropped
}

// PairedNode is what admission needs to know about one Node of this gateway.
type PairedNode struct {
	Platform     string
	AgentVersion string
	Revoked      bool
}

// offer is one peer's proposal, as far as it could be read off the wire.
type offer struct {
	releaseID string
	// raw is the signed manifest exactly as sent. rawOversize is set instead
	// when it was too large to be worth decoding.
	raw         []byte
	rawOversize bool
	chunkSize   int
	chunkCount  int
	totalSize   int64
	// tableSHA is the hash the peer says its chunk table has; tableOK is
	// whether that field was a SHA-256 at all.
	tableSHA [sha256.Size]byte
	tableOK  bool
}

// candidate is an offer on its way through admission. manifest and artifact
// are only filled in once the signature has verified, so no later step can
// act on fields nobody signed.
type candidate struct {
	offer
	manifest proto.OTAManifest
	artifact proto.OTAArtifact
	version  proto.Version
	// completesStored: the store already holds this exact manifest without
	// its artifact, so the download fills a gap rather than adding a release.
	completesStored bool
}

type admissionStep struct {
	name  string
	check func(*Engine, *candidate) *refusal
}

// admissionOrder is the order the checks run in. It is data rather than a
// sequence of statements so that the order itself is something a test can
// hold still: see TestAdmissionRefusesInTheDocumentedOrder.
var admissionOrder = []admissionStep{
	{"manifest_size", (*Engine).checkManifestSize},
	{"signature", (*Engine).checkSignature},
	{"schema", (*Engine).checkSchema},
	{"target", (*Engine).checkTarget},
	{"version", (*Engine).checkVersion},
	{"artifact", (*Engine).checkArtifact},
	{"cache", (*Engine).checkCache},
}

// admitLocked runs an offer through every check and returns the first refusal.
func (e *Engine) admitLocked(proposal offer) (*candidate, *refusal) {
	admitted := &candidate{offer: proposal}
	for _, step := range admissionOrder {
		if refused := step.check(e, admitted); refused != nil {
			return nil, refused
		}
	}
	return admitted, nil
}

func (e *Engine) checkManifestSize(c *candidate) *refusal {
	if c.rawOversize || len(c.raw) == 0 || len(c.raw) > proto.OTAMaxManifestBytes {
		return refuse(ReasonManifestSize, penaltyForged,
			"the manifest is empty or larger than %d bytes", proto.OTAMaxManifestBytes)
	}
	return nil
}

func (e *Engine) checkSignature(c *candidate) *refusal {
	// Without release keys nothing a peer shows can be told from a forgery,
	// and the engine never gets this far (it reports itself disabled). The
	// check is repeated here because this is the line that must never be
	// crossed by accident.
	if len(e.keys) == 0 {
		return refuse(ReasonSignature, penaltyNone, "this gateway has no Node release keys")
	}
	var signed proto.OTASignedManifest
	if err := json.Unmarshal(c.raw, &signed); err != nil || len(signed.Manifest) == 0 {
		return refuse(ReasonSignature, penaltyForged, "not a signed manifest")
	}
	manifest, _, err := proto.VerifyManifestHybrid(signed, e.keys, e.pqKeys, e.policy)
	if err != nil {
		return refuse(ReasonSignature, penaltyForged, "%v", err)
	}
	c.manifest = manifest
	return nil
}

func (e *Engine) checkSchema(c *candidate) *refusal {
	manifest := c.manifest
	if err := proto.ValidateOTAReleaseID(manifest.ReleaseID); err != nil {
		return refuse(ReasonSchema, penaltyPolicy, "the release id is not usable")
	}
	if manifest.ReleaseID != c.releaseID {
		// The envelope named one release and the signed manifest another.
		return refuse(ReasonSchema, penaltyForged, "the offer names %q and its manifest %q", c.releaseID, manifest.ReleaseID)
	}
	if manifest.SchemaVersion != proto.OTASchemaVersion || manifest.ProtocolMajor != proto.ProtocolMajor {
		return refuse(ReasonSchema, penaltyPolicy, "the release is for another schema or protocol generation")
	}
	if len(manifest.Artifacts) != 1 || manifest.Artifacts[0].Kind != proto.OTAArtifactAgent {
		return refuse(ReasonSchema, penaltyPolicy, "the manifest does not carry exactly one agent artifact")
	}
	artifact := manifest.Artifacts[0]
	if artifact.Name == "" || strings.ContainsAny(artifact.Name, `/\`) {
		return refuse(ReasonSchema, penaltyPolicy, "the artifact name is not a plain file name")
	}
	c.artifact = artifact
	return nil
}

func (e *Engine) checkTarget(c *candidate) *refusal {
	if c.manifest.Channel != proto.OTAChannelStable {
		// Only the stable channel is fetched on a stranger's suggestion. A
		// beta is something an administrator chooses and uploads.
		return refuse(ReasonChannel, penaltyPolicy, "channel %q is not fetched from peers", c.manifest.Channel)
	}
	platform, arch := string(c.manifest.Platform), c.artifact.Arch
	for _, target := range e.targetsLocked() {
		if target.Platform != platform {
			continue
		}
		if target.Arch == "" || target.Arch == arch {
			return nil
		}
	}
	return refuse(ReasonPlatform, penaltyPolicy, "no paired or allowed device is %s/%s", platform, arch)
}

var releaseVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func (e *Engine) checkVersion(c *candidate) *refusal {
	// A release version, in full: no pre-release or build suffix. ParseVersion
	// would read "1.4.0-rc1" as 1.4.0, which is how a release candidate gets
	// to outrank the release it was a candidate for.
	if !releaseVersionPattern.MatchString(c.manifest.Version) {
		return refuse(ReasonDowngrade, penaltyPolicy, "%q is not a release version", c.manifest.Version)
	}
	version, err := proto.ParseVersion(c.manifest.Version)
	if err != nil {
		return refuse(ReasonDowngrade, penaltyPolicy, "%q is not a release version", c.manifest.Version)
	}
	c.version = version
	platform, channel := string(c.manifest.Platform), string(c.manifest.Channel)

	for _, stored := range e.store.Releases() {
		if stored.ReleaseID == c.releaseID {
			raw, _, err := e.store.SignedManifest(stored.ReleaseID)
			if err != nil || !bytes.Equal(raw, c.raw) {
				// The store never replaces a manifest under a release id it
				// already holds; fetching this one could only end in a
				// refused import.
				return refuse(ReasonDowngrade, penaltyPolicy, "a different manifest is already stored as %s", c.releaseID)
			}
			if stored.ArtifactReady {
				return refuse(ReasonDowngrade, penaltyNone, "%s is already stored", c.releaseID)
			}
			c.completesStored = true
			continue
		}
		if stored.Platform != platform || stored.Channel != channel {
			continue
		}
		have, err := proto.ParseVersion(stored.Version)
		if err != nil {
			continue
		}
		if version.Compare(have) <= 0 {
			return refuse(ReasonDowngrade, penaltyPolicy, "%s is not newer than the stored %s", version, have)
		}
	}
	// A download already under way counts like a stored release: the same
	// version is not fetched twice under two release ids.
	for _, active := range e.downloads {
		if active.releaseID == c.releaseID || active.platform != platform || active.channel != channel {
			continue
		}
		if have, err := proto.ParseVersion(active.version); err == nil && version.Compare(have) <= 0 {
			return refuse(ReasonDowngrade, penaltyPolicy, "%s is not newer than %s, which is being fetched", version, have)
		}
	}
	for _, node := range e.pairedNodes() {
		if node.Revoked || node.Platform != platform {
			continue
		}
		running, err := proto.ParseVersion(node.AgentVersion)
		if err != nil {
			// A device that has not reported a readable version is no
			// evidence either way.
			continue
		}
		if version.Compare(running) < 0 {
			return refuse(ReasonDowngrade, penaltyPolicy, "%s is older than the %s a paired device runs", version, running)
		}
	}
	return nil
}

func (e *Engine) checkArtifact(c *candidate) *refusal {
	digest, err := hex.DecodeString(c.artifact.SHA256)
	if err != nil || len(digest) != sha256.Size {
		return refuse(ReasonArtifact, penaltyPolicy, "the manifest carries no usable SHA-256")
	}
	if c.artifact.Size <= 0 || c.artifact.Size > proto.OTAMaxArtifactBytes {
		return refuse(ReasonArtifact, penaltyPolicy, "the artifact is %d bytes; the limit is %d", c.artifact.Size, proto.OTAMaxArtifactBytes)
	}
	// What the peer says about the transfer has to describe the artifact the
	// manifest signs for, in a shape this protocol allows.
	if !c.tableOK || c.totalSize != c.artifact.Size || !validGeometry(c.totalSize, c.chunkSize, c.chunkCount) {
		return refuse(ReasonArtifact, penaltyPolicy, "the offered transfer does not describe the signed artifact")
	}
	return nil
}

func (e *Engine) checkCache(c *candidate) *refusal {
	if stored, maximum := e.store.Capacity(); !c.completesStored && stored >= maximum {
		return refuse(ReasonStoreFull, penaltyNone, "the release store already holds %d releases", stored)
	}
	if len(e.downloads) >= e.maxDownloads && !e.evictStalledLocked() {
		return refuse(ReasonBusy, penaltyNone, "%d downloads are already in progress", len(e.downloads))
	}
	for e.reservedLocked()+c.totalSize > e.cacheCap {
		if !e.evictStalledLocked() {
			return refuse(ReasonCacheFull, penaltyNone, "cache full: %d of %d bytes are in use and the artifact is %d",
				e.reservedLocked(), e.cacheCap, c.totalSize)
		}
	}
	return nil
}
