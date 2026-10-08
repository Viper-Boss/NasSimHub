package ota

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/human-agent65535/modemdeck/internal/release"
)

// DefaultSignedManifestURLTemplate is where a release's signed manifest lives.
//
// A NEW path, deliberately, rather than replacing release-manifest.json in
// place. Gateways already in the field validate that file against schema 2; if
// the published file became schema 3 they would reject it and report "no update
// available" forever, with nothing to indicate why. Leaving the old file where
// it is lets those builds keep working until they are updated by some other
// route, and the two files can coexist for as long as that takes.
const DefaultSignedManifestURLTemplate = "https://raw.githubusercontent.com/Viper-Boss/NasSimHub/%s/deploy/release.signed.json"

// SignedManifestLoader fetches a release manifest and verifies its signature
// before anybody can act on it.
//
// The URL is allowed to be operator-configurable, and after this change that is
// no longer a risk worth caring about: whoever controls the URL controls which
// bytes arrive, not which bytes are accepted. That is the property that makes
// the other two distribution channels possible at all -- a peer over NKN and a
// file off a USB stick are the same situation as this one, and none of them is
// trusted.
type SignedManifestLoader struct {
	urlTemplate     string
	publicKeyBase64 string
	client          HTTPClient
}

func NewSignedManifestLoader(
	urlTemplate, publicKeyBase64 string,
	client HTTPClient,
) *SignedManifestLoader {
	if strings.TrimSpace(urlTemplate) == "" {
		// The dual-signed manifest is published beside the classical one,
		// under its own name; see release.PublishedManifestName.
		urlTemplate = strings.TrimSuffix(DefaultSignedManifestURLTemplate, "release.signed.json") + release.PublishedManifestName()
	}
	if strings.TrimSpace(publicKeyBase64) == "" {
		publicKeyBase64 = release.SigningPublicKey
	}
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &SignedManifestLoader{
		urlTemplate:     urlTemplate,
		publicKeyBase64: publicKeyBase64,
		client:          client,
	}
}

// Load fetches, verifies, and only then converts.
//
// Every failure returns ErrManifestUnavailable, so the UI reports "cannot check
// for updates" rather than inventing a version. A signature failure is NOT
// softened into "no update": it is joined onto the error so it reaches the log,
// because a manifest that fails to verify is the one event here worth looking
// at -- it means either a botched publish or somebody trying something.
func (loader *SignedManifestLoader) Load(
	ctx context.Context,
	version string,
) (ReleaseManifest, error) {
	if !stableTagPattern.MatchString(version) {
		return ReleaseManifest{}, ErrManifestUnavailable
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, fmt.Sprintf(loader.urlTemplate, version), nil)
	if err != nil {
		return ReleaseManifest{}, ErrManifestUnavailable
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "NasSimHub-Guardian")
	response, err := loader.client.Do(request)
	if err != nil {
		return ReleaseManifest{}, ErrManifestUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ReleaseManifest{}, ErrManifestUnavailable
	}

	// Read whole, bounded, then verified the way this build requires: a build
	// with a post-quantum release key accepts only a dual-signed manifest.
	document, err := io.ReadAll(io.LimitReader(response.Body, int64(release.MaxPublishedBytes())+1))
	if err != nil || len(document) > release.MaxPublishedBytes() {
		return ReleaseManifest{}, ErrManifestUnavailable
	}
	verified, err := release.VerifyPublished(document, loader.publicKeyBase64)
	if err != nil {
		return ReleaseManifest{}, fmt.Errorf("%w: %w", ErrManifestUnavailable, err)
	}

	// The version in the manifest has to be the version that was asked for.
	// Without this a signed manifest for ANY release would be accepted as the
	// manifest for this one -- a genuine signature replayed to install an old
	// version with a known hole, which is the one attack a signature alone does
	// not stop.
	if verified.ReleaseVersion != version {
		return ReleaseManifest{}, fmt.Errorf(
			"%w: manifest is for %s but %s was requested",
			ErrManifestUnavailable, verified.ReleaseVersion, version)
	}
	return adaptSignedManifest(verified)
}

// adaptSignedManifest maps the verified manifest onto the structure the planner
// already consumes, so this change does not ripple through the controller.
//
// Every first-party image must be present and digest-pinned or the whole
// manifest is refused. A missing component cannot be silently skipped: the
// planner would then simply not update it, leaving a gateway running a mixture
// of two releases that nobody tested together.
func adaptSignedManifest(verified release.Manifest) (ReleaseManifest, error) {
	var manifest ReleaseManifest
	manifest.SchemaVersion = signedSchemaMarker

	type slot struct {
		repository *string
		digest     *string
	}
	slots := map[string]slot{
		"api":      {&manifest.Images.API, &manifest.Digests.API},
		"web":      {&manifest.Images.Web, &manifest.Digests.Web},
		"hardware": {&manifest.Images.Hardware, &manifest.Digests.Hardware},
		"updater":  {&manifest.Images.Updater, &manifest.Digests.Updater},
	}
	for name, target := range slots {
		image, ok := verified.Image(name)
		if !ok {
			return ReleaseManifest{}, fmt.Errorf(
				"%w: signed manifest names no %s image", ErrManifestUnavailable, name)
		}
		*target.repository = image.Repository
		*target.digest = image.Digest
	}

	// cloudflared is optional: it is only present in deployments that enable
	// the Cloudflare overlay, and a release that does not move it does not have
	// to mention it.
	if image, ok := verified.Image("cloudflared"); ok {
		manifest.Cloudflared.Image = image.Repository
		manifest.Cloudflared.Version = image.Tag
		manifest.Cloudflared.Digest = image.Digest
	}

	// The same repository allow-list the unsigned manifest was held to. It is
	// not redundant with the signature: it bounds what a mis-generated -- or
	// maliciously generated, should the signing key ever leak -- manifest can
	// point a gateway at, and costs one comparison.
	if err := manifest.validateRepositories(); err != nil {
		return ReleaseManifest{}, fmt.Errorf("%w: %w", ErrManifestUnavailable, err)
	}
	return manifest, nil
}
