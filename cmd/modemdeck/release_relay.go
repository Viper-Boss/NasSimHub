package main

import (
	"context"

	"github.com/human-agent65535/modemdeck/internal/releasechannel"
)

// channelRelay is the release relay of a gateway with no updater: the peer
// networks hand what they heard to this process's own channel.
type channelRelay struct {
	channel *releasechannel.Channel
}

func (relay channelRelay) OfferRelease(_ context.Context, kind string, signed []byte, peers int) error {
	_, err := relay.channel.Offer(kind, signed, peers)
	return err
}

// SignedRelease checks first. Without an updater nothing else asks this
// channel on a schedule, so a gateway nobody had opened the settings page on
// would have nothing to share; the check is cached for hours, so asking here
// costs nothing when it has already run.
func (relay channelRelay) SignedRelease(ctx context.Context) ([]byte, error) {
	relay.channel.Check(ctx)
	return relay.channel.Signed(), nil
}
