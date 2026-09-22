package release

import (
	"sort"
	"strconv"
	"strings"
)

// Domain is the first line of every signed release manifest.
//
// THIS LINE IS THE WHOLE REASON ONE KEY CAN SAFELY SIGN TWO KINDS OF THING.
//
// The same Ed25519 key signs licence certificates (internal/license) and these
// manifests. Without domain separation that is a cross-protocol replay waiting
// to happen: a signature is only a signature over bytes, so if the two
// encodings could ever produce the same byte string, a certificate obtained
// legitimately could be presented as a release manifest, or the reverse, and
// both would verify.
//
// The certificate encoder's first line is always "v=<integer>" -- see
// Certificate.canonical in internal/license, where "v" is written first and
// unconditionally. A byte string beginning with the line below therefore cannot
// be produced by it, whatever the certificate's contents, and verify() here
// requires that line. The exclusion runs both ways:
//
//	a certificate's bytes never start with this line, so they fail here;
//	these bytes never start with "v=", so they fail there.
//
// Both directions are asserted in TestTheTwoSignatureDomainsCannotBeConfused.
//
// The licence side deliberately did NOT get a matching prefix added. It would
// be tidier, but it would change what every certificate hashes to and so
// invalidate the golden vector duplicated in the gateway, the issuing console
// and the beta tree -- a three-repository lockstep change that buys no security
// the one-sided prefix does not already provide.
//
// If the encoding ever changes meaning, this string changes with it. That is
// what the trailing version is for: old and new signatures then live in
// different domains rather than silently competing.
const Domain = "nsh-domain=release-manifest-v1"

// canonical renders the manifest as the exact bytes that get signed.
//
// Deliberately not json.Marshal, for the same reasons as the licence
// certificate: a signature has to survive a Go version bump, a struct field
// being reordered, and being reimplemented in the signing tool, and JSON
// guarantees none of that. Every field appears in a fixed order whether or not
// it is empty, so adding a field later cannot quietly change what an older
// manifest hashed to.
//
// Images are sorted by name, so the order they happen to sit in a JSON array
// cannot change the signature. Their fields are space-separated on one line,
// which is safe because validate() has already refused any name, repository,
// tag or digest containing a space or a newline -- the two separators this
// encoding uses.
func (manifest Manifest) canonical() ([]byte, error) {
	if err := manifest.validate(); err != nil {
		return nil, err
	}

	images := append([]Image(nil), manifest.Images...)
	sort.Slice(images, func(a, b int) bool { return images[a].Name < images[b].Name })

	var builder strings.Builder
	write := func(key, value string) {
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(value)
		builder.WriteByte('\n')
	}

	// The domain line is written whole rather than through write(), because it
	// already contains its own '=' and must appear byte for byte as declared.
	builder.WriteString(Domain)
	builder.WriteByte('\n')

	write("schema", strconv.Itoa(manifest.SchemaVersion))
	write("release", manifest.ReleaseVersion)
	write("created_at", strconv.FormatInt(manifest.CreatedAt, 10))
	write("minimum_version", manifest.MinimumVersion)
	for _, image := range images {
		write("image", strings.Join(
			[]string{image.Name, image.Repository, image.Tag, image.Digest}, " "))
	}
	return []byte(builder.String()), nil
}

// Canonical exposes the signed bytes.
//
// Exported so the signing tool signs the same bytes this package verifies, and
// so a test can compare them against a pinned golden vector rather than against
// whatever the code currently happens to produce.
func (manifest Manifest) Canonical() ([]byte, error) {
	return manifest.canonical()
}
