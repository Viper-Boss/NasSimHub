package release

import "sort"

// The images a release is made of, and the only repositories a manifest may
// name.
//
// ONE list, used by three things that must never disagree: the signing tool
// builds manifests from it, the gateway's manifest adapter refuses anything
// outside it, and the guard test checks nothing upstream survives in it. They
// used to be three copies of the same four strings, which is how a set of
// constants like this drifts -- somebody updates the signer, the verifier keeps
// refusing, and the obvious "fix" is to loosen the verifier.
//
// These are THIS project's packages, not the upstream project's.
//
// That distinction is the whole point of this file. The update path shipped
// pointing at ghcr.io/human-agent65535/*, which belongs to the upstream work
// this repository derives from (see NOTICE.md) and is not under this project's
// control. Nothing about that was obviously wrong to read -- the names match
// the Go module path, which is also upstream's -- and the consequence only
// appeared when an update actually ran: upstream tags a release, every gateway
// pulls upstream's images, and this product is replaced by the one it was
// forked from. Device adapters, home-SIM features, licensing: gone, on a
// routine update.
//
// So the rule is now explicit rather than remembered: a release is built from
// images this project publishes, pinned by digest, named in a manifest this
// project signs. Upstream is a source of code and nothing else. No part of the
// update path may reference it, and TestTheUpdatePathNamesNothingUpstream in
// cmd/modemdeck fails the build if that changes.
const (
	// RegistryNamespace is where this project's release images live.
	//
	// GHCR package visibility is set per package and is independent of the
	// repository's, so these can be public while Viper-Boss/NasSimHub stays
	// private -- which is what makes pulling images work without publishing
	// the source.
	RegistryNamespace = "ghcr.io/viper-boss"

	RepositoryAPI      = RegistryNamespace + "/nassimhub"
	RepositoryWeb      = RegistryNamespace + "/nassimhub-web"
	RepositoryHardware = RegistryNamespace + "/nassimhub-hardware"
	RepositoryUpdater  = RegistryNamespace + "/nassimhub-updater"

	// RepositoryCloudflared is a third-party image and deliberately not under
	// the namespace above: it is pulled from its publisher, pinned by a digest
	// this project signs. Signing a digest for somebody else's image is not an
	// endorsement of their tag -- it records which of their builds was tested.
	RepositoryCloudflared = "cloudflare/cloudflared"
)

// Repositories maps component name to the repository it must come from.
//
// Cloudflared is deliberately absent: it is optional in a manifest, so holding
// it in the same map as the four required components would invite code that
// iterates the map and treats them alike.
var Repositories = map[string]string{
	"api":      RepositoryAPI,
	"web":      RepositoryWeb,
	"hardware": RepositoryHardware,
	"updater":  RepositoryUpdater,
}

// Components lists the required component names in a stable order, so error
// messages and generated manifests do not shuffle between runs.
func Components() []string {
	names := make([]string, 0, len(Repositories))
	for name := range Repositories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
