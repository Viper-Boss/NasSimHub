package ota

import (
	"context"
	"fmt"

	"github.com/human-agent65535/modemdeck/internal/release"
)

// LatestManifestSource is the release channel as the planner sees it.
type LatestManifestSource interface {
	Latest(version string) (release.Manifest, error)
}

// ChannelManifests adapts the release channel to the planner.
//
// It does not fetch. The channel already verified a manifest when it answered
// the check; the plan is built from that same document, so what the operator
// was shown and what gets installed cannot come from two different downloads.
type ChannelManifests struct {
	Channel LatestManifestSource
}

func (loader ChannelManifests) Load(_ context.Context, version string) (ReleaseManifest, error) {
	if loader.Channel == nil {
		return ReleaseManifest{}, ErrManifestUnavailable
	}
	verified, err := loader.Channel.Latest(version)
	if err != nil {
		return ReleaseManifest{}, fmt.Errorf("%w: %w", ErrManifestUnavailable, err)
	}
	return adaptSignedManifest(verified)
}
