package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/release"
)

// releaseSign signs a release manifest.
//
// Offline, on the issuer's machine, like every other command here. The gateway
// holds only the public half and there is nothing in the shipped binary that
// could sign anything.
//
// Digests are supplied by the operator rather than looked up from the registry
// by this tool, deliberately. Asking ghcr.io "what does tag v1.4.2 point at
// right now?" and signing the answer would move the trust back onto the
// registry and the network path to it -- the exact arrangement this replaces.
// The digests are read off the push that produced them (`docker push` prints
// each one, and `docker buildx imagetools inspect` reprints them), so what gets
// signed is what the release engineer saw, not what a server said later.
func releaseSign(arguments []string) error {
	set := flag.NewFlagSet("release-sign", flag.ExitOnError)
	keyPath := set.String("key", "", "issuer private key file (hex), as written by keygen")
	version := set.String("version", "", "release version, e.g. v1.4.2")
	minimum := set.String("minimum", "", "oldest gateway allowed to install this (optional)")
	out := set.String("out", "", "write the signed manifest here (default: stdout)")
	api := set.String("api", "", "api image digest, sha256:...")
	web := set.String("web", "", "web image digest, sha256:...")
	hardware := set.String("hardware", "", "hardware image digest, sha256:...")
	updater := set.String("updater", "", "updater image digest, sha256:...")
	cloudflared := set.String("cloudflared", "", "cloudflared digest, sha256:... (optional)")
	cloudflaredTag := set.String("cloudflared-version", "", "cloudflared tag, required with -cloudflared")
	if err := set.Parse(arguments); err != nil {
		return err
	}

	key, err := loadPrivateKey(*keyPath)
	if err != nil {
		return err
	}

	digests := map[string]string{
		"api":      strings.TrimSpace(*api),
		"web":      strings.TrimSpace(*web),
		"hardware": strings.TrimSpace(*hardware),
		"updater":  strings.TrimSpace(*updater),
	}
	// Built from internal/release.Repositories, the same list the gateway holds
	// a manifest to. A signer with its own copy of these names would eventually
	// emit manifests every gateway refuses.
	var images []release.Image
	for _, component := range release.Components() {
		images = append(images, release.Image{
			Name:       component,
			Repository: release.Repositories[component],
			Tag:        *version,
			Digest:     digests[component],
		})
	}
	if strings.TrimSpace(*cloudflared) != "" {
		if strings.TrimSpace(*cloudflaredTag) == "" {
			return fmt.Errorf("-cloudflared needs -cloudflared-version")
		}
		images = append(images, release.Image{
			Name:       "cloudflared",
			Repository: release.RepositoryCloudflared,
			Tag:        strings.TrimSpace(*cloudflaredTag),
			Digest:     strings.TrimSpace(*cloudflared),
		})
	}

	// Named up front rather than letting validate() report them one at a time:
	// a release engineer running this at the end of a build wants every missing
	// flag in one go, not four rounds of trial and error.
	var missing []string
	for _, image := range images {
		if image.Digest == "" {
			missing = append(missing, "-"+image.Name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return fmt.Errorf("every image must be pinned by digest; missing: %s\n"+
			"Read them from the push output, or:\n"+
			"  docker buildx imagetools inspect <repo>:%s --format '{{.Manifest.Digest}}'",
			strings.Join(missing, " "), *version)
	}

	manifest := release.Manifest{
		SchemaVersion:  release.SchemaVersion,
		ReleaseVersion: strings.TrimSpace(*version),
		CreatedAt:      time.Now().Unix(),
		MinimumVersion: strings.TrimSpace(*minimum),
		Images:         images,
	}

	signed, err := release.Sign(manifest, key)
	if err != nil {
		return err
	}
	document, err := json.MarshalIndent(signed, "", "  ")
	if err != nil {
		return err
	}
	document = append(document, '\n')

	if strings.TrimSpace(*out) == "" {
		_, err = os.Stdout.Write(document)
		return err
	}
	// O_EXCL: re-signing over an existing manifest would replace a document
	// that may already have been published, and two different signed manifests
	// for one version is a situation with no good resolution.
	file, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	defer file.Close()
	if _, err := file.Write(document); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}

	public, _ := key.Public().(ed25519.PublicKey)
	fmt.Fprintf(os.Stderr, "signed %s -> %s\n", manifest.ReleaseVersion, *out)
	fmt.Fprintf(os.Stderr, "signing public key: %s\n", base64.StdEncoding.EncodeToString(public))
	fmt.Fprintln(os.Stderr,
		"This must match the key compiled into the gateways that will install it\n"+
			"(internal/license/issuer_key.go). If it does not, every gateway refuses this release.")
	return nil
}
