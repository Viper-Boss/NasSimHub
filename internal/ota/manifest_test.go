package ota

import (
	"strings"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/release"
)

// A manifest may only name this project's own image repositories.
//
// The schema and digest checks that used to sit beside this one have moved
// into internal/release, where they apply to the signed document itself. What
// stays here is the allow-list, because it is about which repositories this
// BUILD will accept regardless of who signed the manifest.
func TestAManifestMayOnlyNameThisProjectsRepositories(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*ReleaseManifest){
		"unexpected updater repository": func(manifest *ReleaseManifest) {
			manifest.Images.Updater = "ghcr.io/example/nassimhub-updater"
		},
		// The case this whole rename exists for: upstream's image must be
		// refused, not merely different. upstream-guard:allow
		"upstream api repository": func(manifest *ReleaseManifest) {
			manifest.Images.API = "ghcr.io/human-agent65535/modemdeck" // upstream-guard:allow
		},
		"empty hardware repository": func(manifest *ReleaseManifest) {
			manifest.Images.Hardware = ""
		},
		"cloudflared from elsewhere": func(manifest *ReleaseManifest) {
			manifest.Cloudflared.Image = "docker.io/attacker/cloudflared"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			manifest := testManifest(testDigest("d"))
			mutate(&manifest)
			if err := manifest.validateRepositories(); err == nil {
				t.Fatal("an unsafe release manifest was accepted")
			}
		})
	}
}

// The allow-list must be the one in internal/release, not a second copy that
// can drift away from what the signing tool builds manifests from.
func TestTheAllowListIsTheSharedOne(t *testing.T) {
	t.Parallel()
	manifest := testManifest(testDigest("d"))
	if err := manifest.validateRepositories(); err != nil {
		t.Fatalf("the shared repository list does not accept a well-formed manifest: %v", err)
	}
	for _, component := range release.Components() {
		if !strings.HasPrefix(release.Repositories[component], release.RegistryNamespace+"/") {
			t.Errorf("%s is published outside this project's namespace: %s",
				component, release.Repositories[component])
		}
	}
}
