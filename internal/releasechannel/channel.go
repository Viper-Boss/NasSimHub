// Package releasechannel answers "what is the newest release", from signed
// documents only.
//
// # WHY THIS REPLACED THE GITHUB RELEASES CHECK
//
// The previous check asked api.github.com for the latest release of
// Viper-Boss/NasSimHub, then fetched that release's manifest from
// raw.githubusercontent.com. That repository is private. An unauthenticated
// request for a private repository's releases answers 404, and so does a raw
// file inside it, so on every gateway the check reported "no release" and no
// update could ever be offered. Nothing was wrong with the code; it pointed at
// a door that is locked from the outside.
//
// Making the source repository public is not the fix -- it is private for a
// reason. So releases are published separately: a small PUBLIC repository
// holding nothing but signed manifests, served by GitHub and by CDN mirrors of
// GitHub. Publishing there discloses a version number and four image digests,
// which the images' own registry already discloses to anyone who pulls them.
//
// # WHY MORE THAN ONE URL, AND WHY THE HIGHEST VERIFIED VERSION WINS
//
// The gateways that ask are mostly in mainland China, where
// raw.githubusercontent.com is unreliable and a CDN mirror of the same file
// often is not. Each mirror is an untrusted party: a manifest is believed
// because it verifies against the key compiled into this build, never because
// of where it came from, so adding a mirror cannot add a way in.
//
// What a mirror CAN still do is lie by omission -- serve an old, genuinely
// signed release and keep the gateway there. Asking every mirror and taking the
// highest version that verifies means one honest, reachable mirror is enough to
// defeat any number of stale or hostile ones. A mirror serving an older release
// than the rest is reported as such, not trusted as the answer.
package releasechannel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/internal/release"
	"github.com/human-agent65535/modemdeck/internal/updatecheck"
)

// DefaultURLs are where the stable channel is published.
//
// One public repository, three routes to the same file: GitHub's own raw host,
// and jsDelivr's two front ends for GitHub content, which are usually the ones
// reachable from inside China. An operator can replace or extend this list
// (MODEMDECK_RELEASE_CHANNEL_URLS) with a mirror of their own -- a Gitee copy,
// an object bucket, a file on the LAN -- and that is safe for the reason given
// at the top of this file.
var DefaultURLs = []string{
	"https://raw.githubusercontent.com/Viper-Boss/nassimhub-releases/main/stable.signed.json",
	"https://cdn.jsdelivr.net/gh/Viper-Boss/nassimhub-releases@main/stable.signed.json",
	"https://fastly.jsdelivr.net/gh/Viper-Boss/nassimhub-releases@main/stable.signed.json",
}

// DefaultReleasePageURL is where a human reads what a release changed.
//
// Release notes are deliberately NOT in the signed manifest and not shown in
// the gateway's own UI. Text shown inside the product is text an attacker would
// like to write ("support has moved, call this number"), and notes that are
// displayed would therefore have to be signed too -- a schema change for prose.
// A link to the public repository costs nothing and cannot be forged into the
// product's own pages.
const DefaultReleasePageURL = "https://github.com/Viper-Boss/nassimhub-releases/blob/main/releases/"

const (
	requestTimeout = 10 * time.Second
	successTTL     = 6 * time.Hour
	// Long enough that a page opened five times on a dead network does not
	// wait five times, short enough that fixing the network shows up soon.
	failureTTL = 2 * time.Minute
)

var (
	// ErrNoVerifiedRelease means no mirror produced a manifest that verifies.
	ErrNoVerifiedRelease = errors.New("releasechannel: no verified release is available")
	// ErrBelowMinimum means the newest release refuses to install on this
	// version and an intermediate release has to come first.
	ErrBelowMinimum = errors.New("releasechannel: this installation is older than the release allows")
)

// HTTPClient is the subset of *http.Client used here.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// Options configure a Channel.
type Options struct {
	URLs            []string
	PublicKeyBase64 string
	Client          HTTPClient
	// CurrentVersion is asked on every check rather than fixed at start-up:
	// the updater replaces the running version underneath itself.
	CurrentVersion func() string
	ReleasePageURL string
	Now            func() time.Time
}

// Channel is the stable release channel.
type Channel struct {
	urls           []string
	publicKey      string
	client         HTTPClient
	currentVersion func() string
	releasePage    string
	now            func() time.Time

	mu       sync.Mutex
	snapshot *snapshot
}

type snapshot struct {
	forVersion string
	result     updatecheck.Result
	manifest   *release.Manifest
	expires    time.Time
}

// New builds a channel. An empty URL list means the defaults.
func New(options Options) *Channel {
	urls := options.URLs
	if len(urls) == 0 {
		urls = DefaultURLs
	}
	publicKey := strings.TrimSpace(options.PublicKeyBase64)
	if publicKey == "" {
		publicKey = release.SigningPublicKey
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	current := options.CurrentVersion
	if current == nil {
		current = func() string { return "dev" }
	}
	page := strings.TrimSpace(options.ReleasePageURL)
	if page == "" {
		page = DefaultReleasePageURL
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Channel{
		urls:           append([]string(nil), urls...),
		publicKey:      publicKey,
		client:         client,
		currentVersion: current,
		releasePage:    page,
		now:            now,
	}
}

// ParseURLs reads an operator-supplied list: comma or whitespace separated,
// http or https only. Anything else is dropped rather than failing start-up,
// and the caller logs what was kept, because an updater that refuses to start
// over a typo in an optional mirror list takes the whole update path down.
func ParseURLs(value string) (kept []string, dropped []string) {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == ';'
	})
	for _, field := range fields {
		parsed, err := url.Parse(field)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			dropped = append(dropped, field)
			continue
		}
		kept = append(kept, field)
	}
	return kept, dropped
}

// Check reports the newest verified release against the running version.
func (channel *Channel) Check(ctx context.Context) updatecheck.Result {
	current := updatecheck.DisplayVersion(channel.currentVersion())

	channel.mu.Lock()
	defer channel.mu.Unlock()
	now := channel.now().UTC()
	if cached := channel.snapshot; cached != nil &&
		cached.forVersion == current &&
		now.Before(cached.expires) &&
		!updatecheck.RefreshRequested(ctx) {
		return cloneResult(cached.result)
	}

	manifest, sources := channel.fetchAll(ctx)
	result := updatecheck.Result{
		Status:         updatecheck.StatusUnavailable,
		CurrentVersion: current,
		CheckedAt:      now.Format(time.RFC3339),
		Sources:        sources,
	}
	ttl := failureTTL
	if manifest == nil {
		result.ErrorCode = "release_channel_unavailable"
		for _, source := range sources {
			if source.State == updatecheck.SourceInvalidSignature {
				// Said separately because it is the one failure worth a
				// human looking at: either a botched publish, or somebody
				// trying something.
				result.ErrorCode = "release_signature_invalid"
				break
			}
		}
	} else {
		ttl = successTTL
		latest := manifest.ReleaseVersion
		result.LatestVersion = latest
		result.ReleaseName = "NasSimHub " + latest
		result.ReleaseURL = channel.releasePage + url.PathEscape(latest) + ".md"
		result.PublishedAt = time.Unix(manifest.CreatedAt, 0).UTC().Format(time.RFC3339)
		result.Status = updatecheck.StatusDevelopment
		if order, ok := updatecheck.CompareVersions(current, latest); ok {
			switch order {
			case -1:
				result.Status = updatecheck.StatusUpdateAvailable
			case 0:
				result.Status = updatecheck.StatusUpToDate
			}
		}
		if result.Status == updatecheck.StatusUpdateAvailable && belowMinimum(current, manifest.MinimumVersion) {
			result.ErrorCode = "release_requires_newer_installation"
		}
	}

	channel.snapshot = &snapshot{
		forVersion: current,
		result:     result,
		manifest:   manifest,
		expires:    now.Add(ttl),
	}
	return cloneResult(result)
}

// Latest returns the verified manifest behind the last check, if it is for
// the version asked. It never fetches: an install plan must be built from the
// very document the operator was shown, not from whatever a mirror serves a
// minute later.
func (channel *Channel) Latest(version string) (release.Manifest, error) {
	current := updatecheck.DisplayVersion(channel.currentVersion())
	channel.mu.Lock()
	defer channel.mu.Unlock()
	cached := channel.snapshot
	if cached == nil || cached.manifest == nil || cached.manifest.ReleaseVersion != version {
		return release.Manifest{}, ErrNoVerifiedRelease
	}
	if belowMinimum(current, cached.manifest.MinimumVersion) {
		return release.Manifest{}, fmt.Errorf("%w: %s needs at least %s",
			ErrBelowMinimum, version, cached.manifest.MinimumVersion)
	}
	return *cached.manifest, nil
}

func belowMinimum(current, minimum string) bool {
	if strings.TrimSpace(minimum) == "" {
		return false
	}
	order, ok := updatecheck.CompareVersions(current, minimum)
	// A development build has no comparable version; the floor exists to
	// protect installations, and a developer installing onto a dev build can
	// be trusted to know what they are doing.
	return ok && order < 0
}

type fetched struct {
	index    int
	source   updatecheck.Source
	manifest *release.Manifest
}

// fetchAll asks every mirror at once and keeps the highest verified version.
//
// Concurrently, because the mirrors that fail usually fail by timing out, and
// asking them in turn would make one dead mirror cost every check ten seconds.
func (channel *Channel) fetchAll(ctx context.Context) (*release.Manifest, []updatecheck.Source) {
	results := make(chan fetched, len(channel.urls))
	for index, address := range channel.urls {
		go func(index int, address string) {
			source, manifest := channel.fetchOne(ctx, address)
			results <- fetched{index: index, source: source, manifest: manifest}
		}(index, address)
	}
	sources := make([]updatecheck.Source, len(channel.urls))
	var best *release.Manifest
	for range channel.urls {
		result := <-results
		sources[result.index] = result.source
		if result.manifest == nil {
			continue
		}
		if best == nil {
			best = result.manifest
			continue
		}
		if order, ok := updatecheck.CompareVersions(best.ReleaseVersion, result.manifest.ReleaseVersion); ok && order < 0 {
			best = result.manifest
		}
	}
	return best, sources
}

func (channel *Channel) fetchOne(ctx context.Context, address string) (updatecheck.Source, *release.Manifest) {
	source := updatecheck.Source{Name: hostOf(address), State: updatecheck.SourceUnreachable}
	requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, address, nil)
	if err != nil {
		return source, nil
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cache-Control", "no-cache")
	request.Header.Set("User-Agent", "NasSimHub-Updater/"+updatecheck.DisplayVersion(channel.currentVersion()))
	response, err := channel.client.Do(request)
	if err != nil {
		return source, nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return source, nil
	}
	manifest, err := release.VerifyJSON(response.Body, channel.publicKey)
	switch {
	case errors.Is(err, release.ErrBadSignature):
		source.State = updatecheck.SourceInvalidSignature
		return source, nil
	case err != nil:
		source.State = updatecheck.SourceMalformed
		return source, nil
	}
	source.State = updatecheck.SourceVerified
	source.Version = manifest.ReleaseVersion
	return source, &manifest
}

// hostOf names a mirror by host only. The full URL may carry credentials for a
// private mirror, and the result is shown in a browser.
func hostOf(address string) string {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" {
		return "mirror"
	}
	return parsed.Hostname()
}

func cloneResult(result updatecheck.Result) updatecheck.Result {
	result.Sources = append([]updatecheck.Source(nil), result.Sources...)
	return result
}
