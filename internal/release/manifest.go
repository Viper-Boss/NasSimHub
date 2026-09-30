// Package release verifies signed release manifests.
//
// What a release manifest is for: it names, for one released version, the exact
// container image every component must run, pinned by digest. It is signed, so
// a gateway can be handed one by an untrusted party -- a random peer, a mirror,
// a USB stick -- and still know whether to believe it.
//
// That last property is the point of the whole package, and it is what the
// previous arrangement lacked. Before this, an update was trusted because it
// arrived over TLS from three separate hosts:
//
//	api.github.com          said which version was latest
//	raw.githubusercontent   served an unsigned manifest
//	ghcr.io                 resolved a MUTABLE TAG to a digest, at install time
//
// Nothing was signed, so each of those hosts -- and anyone who could re-point
// that tag, or present a certificate a gateway's trust store accepted -- could
// choose what every gateway installed. Pinning digests in a signed manifest
// collapses all three into one question with a cryptographic answer, and makes
// the transport irrelevant: bytes are either the ones the signer named, or they
// are refused.
package release

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// SchemaVersion is the only schema this build accepts.
//
// Refusing every other value is deliberate, and it is why the signed form has
// a version at all: a verifier that skipped unknown fields would accept a
// manifest whose meaning it does not fully understand, and a field added later
// could then change what an install does without changing what was verified.
// A newer manifest is not a security failure, so it gets its own error.
const SchemaVersion = 3

const (
	maxManifestBytes = 64 << 10
	maxImages        = 16
)

// MaxManifestBytes is the largest signed manifest this package will read. A
// transport carrying manifests between gateways uses it as its own ceiling,
// so nothing bigger than a manifest can ever travel as one.
const MaxManifestBytes = maxManifestBytes

var (
	// ErrMalformed covers anything structurally wrong, including a manifest
	// this build is too old to understand.
	ErrMalformed = errors.New("release: manifest is malformed")
	// ErrBadSignature means the manifest was altered, or signed by someone
	// else. It is never a reason to install anything.
	ErrBadSignature = errors.New("release: signature does not verify")
	// ErrUnsupportedSchema separates "too new" from "wrong", because the
	// remedy differs: a newer manifest means update the gateway first.
	ErrUnsupportedSchema = errors.New("release: unsupported manifest schema")

	digestPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	// A component name goes into the canonical encoding as a bare token, so it
	// is restricted to characters that cannot disturb the encoding.
	namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	// A repository reference, deliberately narrow: lowercase registry path
	// segments only. It is not a general OCI reference parser, because this
	// field is compared against an allow-list anyway and a permissive parser
	// here would only widen what a signed manifest could ask for.
	repositoryPattern = regexp.MustCompile(`^[a-z0-9.-]+(:[0-9]+)?(/[a-z0-9._-]+)+$`)
	tagPattern        = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)
)

// Image is one component's pinned image for this release.
//
// Digest is required for every image, with no exception and no fallback to
// resolving the tag. That is the substantive change from the previous manifest,
// where only cloudflared carried a digest and the four first-party images were
// resolved from their tags at install time -- which meant a re-pointed tag
// reached every gateway on its next update without the manifest changing at
// all. Tag is kept for display and for building the reference a human reads;
// it is never what decides which bytes get installed.
type Image struct {
	Name       string `json:"name"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Digest     string `json:"digest"`
}

// Reference is the pull reference: repository@digest.
//
// By digest, never by tag. This is what gets handed to `docker pull`, and it is
// the reason a compromised registry cannot substitute content: the daemon
// verifies the digest of what it receives.
func (image Image) Reference() string {
	return image.Repository + "@" + image.Digest
}

// Manifest is the signed payload.
type Manifest struct {
	SchemaVersion int `json:"schema_version"`
	// ReleaseVersion is the version these images constitute, e.g. "v1.4.2".
	ReleaseVersion string `json:"release_version"`
	// CreatedAt is when the manifest was signed, unix seconds. It is signed so
	// that it cannot be back-dated by a party replaying an old manifest with a
	// fresh timestamp; comparing it to the installed release is how a rollback
	// attack (offering a genuinely-signed OLD release as the newest) is
	// noticed.
	CreatedAt int64 `json:"created_at"`
	// MinimumVersion is the oldest gateway permitted to install this release,
	// or empty for no floor. It exists so a release that changes the database
	// schema or the manifest format can refuse to land on a build that cannot
	// cope, rather than landing and failing afterwards.
	MinimumVersion string  `json:"minimum_version,omitempty"`
	Images         []Image `json:"images"`
}

// Signed is the envelope: the manifest and its signature together.
//
// One file rather than the manifest.json + manifest.sig pair, because they are
// fetched over a network: two requests can be served by two different hosts, or
// by the same host either side of a publish, and a verifier that has to notice
// that is a verifier with a bug in it. One object cannot be half-updated.
type Signed struct {
	Manifest  Manifest `json:"manifest"`
	Signature string   `json:"signature"`
}

// Image returns the named component's image.
func (manifest Manifest) Image(name string) (Image, bool) {
	for _, image := range manifest.Images {
		if image.Name == name {
			return image, true
		}
	}
	return Image{}, false
}

// validate rejects anything that would make the canonical encoding ambiguous
// or the install target unclear.
//
// Called by both the signer and the verifier, from canonical(), so the two
// cannot disagree about what is acceptable: a manifest that cannot be validated
// cannot be signed either.
func (manifest Manifest) validate() error {
	if manifest.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: got %d, this build accepts %d",
			ErrUnsupportedSchema, manifest.SchemaVersion, SchemaVersion)
	}
	if !versionPattern.MatchString(manifest.ReleaseVersion) {
		return fmt.Errorf("%w: release version %q", ErrMalformed, manifest.ReleaseVersion)
	}
	if manifest.MinimumVersion != "" && !versionPattern.MatchString(manifest.MinimumVersion) {
		return fmt.Errorf("%w: minimum version %q", ErrMalformed, manifest.MinimumVersion)
	}
	if manifest.CreatedAt <= 0 {
		return fmt.Errorf("%w: created_at", ErrMalformed)
	}
	if len(manifest.Images) == 0 {
		return fmt.Errorf("%w: no images", ErrMalformed)
	}
	if len(manifest.Images) > maxImages {
		return fmt.Errorf("%w: too many images", ErrMalformed)
	}
	seen := make(map[string]bool, len(manifest.Images))
	for _, image := range manifest.Images {
		if !namePattern.MatchString(image.Name) {
			return fmt.Errorf("%w: image name %q", ErrMalformed, image.Name)
		}
		// A duplicate name would make Image() return whichever came first
		// while a reader of the JSON saw the other one.
		if seen[image.Name] {
			return fmt.Errorf("%w: duplicate image %q", ErrMalformed, image.Name)
		}
		seen[image.Name] = true
		if !repositoryPattern.MatchString(image.Repository) ||
			strings.Contains(image.Repository, "..") {
			return fmt.Errorf("%w: image %s repository %q", ErrMalformed, image.Name, image.Repository)
		}
		if !tagPattern.MatchString(image.Tag) {
			return fmt.Errorf("%w: image %s tag %q", ErrMalformed, image.Name, image.Tag)
		}
		// "latest" is refused even though the digest is what actually gets
		// pulled, so the tag is only ever displayed. Recording a release as
		// having shipped "latest" destroys the one record of which build it
		// was, and this check existed on the old unsigned manifest -- dropping
		// it while moving the validation would have been a quiet regression.
		if strings.EqualFold(image.Tag, "latest") {
			return fmt.Errorf("%w: image %s is tagged latest, which names no particular build",
				ErrMalformed, image.Name)
		}
		if !digestPattern.MatchString(image.Digest) {
			return fmt.Errorf("%w: image %s digest %q", ErrMalformed, image.Name, image.Digest)
		}
	}
	return nil
}
