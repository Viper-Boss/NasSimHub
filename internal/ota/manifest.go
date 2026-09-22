package ota

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/release"
)

const defaultHTTPTimeout = 8 * time.Second

var (
	ErrManifestUnavailable = errors.New("release manifest is unavailable")
	stableTagPattern       = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	digestPattern          = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// signedSchemaMarker marks a ReleaseManifest whose contents were
// signature-verified.
//
// Not a wire value: nothing publishes it and nothing parses it. It exists so
// the planner can refuse to build an install plan from anything that did not
// come through verification, whichever loader produced it. See Controller.plan.
//
// Negative so it can never collide with a schema number somebody publishes.
const signedSchemaMarker = -3

// ReleaseManifest is what the planner consumes.
//
// There is exactly one way to obtain a populated one: SignedManifestLoader,
// which verifies a signature first. The unsigned HTTP loader that used to sit
// in this file is gone rather than merely unused -- it fetched an unsigned
// document from the upstream project's repository, and leaving it in the tree
// would leave a working, plausible-looking way to reintroduce both problems at
// once.
type ReleaseManifest struct {
	SchemaVersion int
	Images        struct {
		API      string
		Web      string
		Hardware string
		Updater  string
	}
	// Digests pins each first-party image by content.
	//
	// Always populated, always from the signed manifest. An empty digest used
	// to mean "resolve the tag against the registry at install time", which is
	// how a re-pointed tag could reach every gateway without the manifest
	// changing a byte. That resolver is gone; empty now means refuse to plan.
	Digests struct {
		API      string
		Web      string
		Hardware string
		Updater  string
	}
	Cloudflared struct {
		Image   string
		Version string
		Digest  string
	}
}

// Signed reports whether this manifest's contents were signature-verified.
func (manifest ReleaseManifest) Signed() bool {
	return manifest.SchemaVersion == signedSchemaMarker
}

// validateRepositories holds a manifest to the allow-list in internal/release.
//
// Worth having even behind a signature: it bounds what a mis-generated manifest
// can aim a gateway at, and it means a leaked signing key still cannot redirect
// installs to another registry without also changing the shipped binary.
//
// The allow-list is NOT duplicated here. It lives in internal/release beside
// the signing tool's copy of the same question, because three independent
// copies of four repository names is how a signer and a verifier drift apart --
// and the obvious fix when they do is to loosen the verifier.
func (manifest ReleaseManifest) validateRepositories() error {
	actual := map[string]string{
		"api":      manifest.Images.API,
		"web":      manifest.Images.Web,
		"hardware": manifest.Images.Hardware,
		"updater":  manifest.Images.Updater,
	}
	for _, component := range release.Components() {
		if strings.TrimSpace(actual[component]) != release.Repositories[component] {
			return fmt.Errorf("invalid %s image repository: %q is not %s",
				component, actual[component], release.Repositories[component])
		}
	}
	// Only meaningful when cloudflared is named at all: a signed manifest for a
	// release that does not move cloudflared simply omits it.
	if manifest.Cloudflared.Image != "" &&
		manifest.Cloudflared.Image != release.RepositoryCloudflared {
		return fmt.Errorf("invalid cloudflared image repository")
	}
	return nil
}

type ManifestLoader interface {
	Load(context.Context, string) (ReleaseManifest, error)
}

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}
