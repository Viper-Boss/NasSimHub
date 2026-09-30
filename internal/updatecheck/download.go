package updatecheck

// Types shared by the updater, its HTTP client in the api process, and the
// api's client report. They live here, beside Result, because all three
// already import this package and none of them imports the others.

// DownloadState is where a background pre-fetch stands.
type DownloadState string

const (
	DownloadPending     DownloadState = "pending"
	DownloadDownloading DownloadState = "downloading"
	DownloadReady       DownloadState = "ready"
	DownloadFailed      DownloadState = "failed"
)

// Download records the pre-fetch of one release's images.
//
// "Ready" means every image that release changes is on this machine, pinned
// by the digest the signed manifest names, so installing it no longer depends
// on the registry being reachable -- the part of an update that takes minutes
// and fails on bad networks has already happened, and the part the operator
// presses a button for is a restart.
type Download struct {
	Version    string              `json:"version"`
	State      DownloadState       `json:"state"`
	ErrorCode  string              `json:"error_code,omitempty"`
	Components []DownloadComponent `json:"components,omitempty"`
	StartedAt  string              `json:"started_at,omitempty"`
	UpdatedAt  string              `json:"updated_at"`
}

// DownloadComponent is one image of a download.
type DownloadComponent struct {
	Name  string        `json:"name"`
	State DownloadState `json:"state"`
}

// Preferences are the operator's update settings.
//
// One setting today. A struct rather than a bare boolean so a second one does
// not change the wire shape of the first.
type Preferences struct {
	// AutoDownload fetches a new release's images in the background as soon as
	// a signed release is seen. It never installs anything: installing restarts
	// the gateway, and that is the operator's call to make.
	AutoDownload bool `json:"auto_download"`
}

// Source is what one release source said on the last check.
type Source struct {
	// Name is the mirror's host, never the full URL: a URL can carry a token
	// for a private mirror, and this is shown in a browser. For a peer
	// network it is the network's name.
	Name    string `json:"name"`
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
	// Kind says what sort of source this is: an HTTPS mirror, or one of the
	// two peer networks, where the manifest was handed over by other
	// gateways. Empty means a mirror, which is what every source was before
	// the peer networks existed.
	Kind string `json:"kind,omitempty"`
	// Peers is, for a peer network, how many gateways answered with the
	// version reported. It is a count of answers, never of the network.
	Peers int `json:"peers,omitempty"`
}

// Source kinds.
const (
	SourceKindMirror = "mirror"
	SourceKindNKN    = "nkn"
	SourceKindDHT    = "dht"
)

// Source states.
const (
	SourceVerified         = "verified"
	SourceUnreachable      = "unreachable"
	SourceInvalidSignature = "invalid_signature"
	SourceMalformed        = "malformed"
)

// RunningImage is what one component is actually running.
//
// Reported to the issuer in the client report, so "which build is this
// customer on" has an answer that does not depend on the version string the
// build claims for itself.
type RunningImage struct {
	Component string `json:"component"`
	Service   string `json:"service"`
	Present   bool   `json:"present"`
	// Reference is the image reference the container was started from.
	Reference string `json:"reference,omitempty"`
	// ImageID is the local image ID, which is always known.
	ImageID string `json:"image_id,omitempty"`
	// Digest is the registry manifest digest when it is known. It is NOT
	// known for an image loaded from a tarball -- which is how the fnOS
	// package ships its first install -- and is left empty rather than
	// guessed, because a guessed digest is worse than none in a report whose
	// point is to say exactly which bytes are running.
	Digest  string `json:"digest,omitempty"`
	Version string `json:"version,omitempty"`
}
