package main

import (
	"context"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/store"
	"github.com/human-agent65535/modemdeck/internal/trustengine"
)

// Recount after the message commit, before publishing any notification. A
// five-minute background sample alone would let a burst bypass the limit.
func (n *nassimhubNodes) allowOTPForward(ctx context.Context, message store.Message) bool {
	rest, node := strings.CutPrefix(strings.TrimSpace(message.EndpointLineID), store.NasSimHubNodeEndpointPrefix)
	if !node {
		return !strings.HasPrefix(message.EndpointMessageID, "nsh-in:")
	}
	id, slot, valid := strings.Cut(rest, ":")
	if !valid || id == "" || slot != "0" || n.trust == nil || n.stats == nil {
		return false
	}
	n.otpMu.Lock()
	defer n.otpMu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	stats, err := n.stats.CommunicationStatsForNode(ctx, time.Now(), 24, id)
	if err != nil {
		return false
	}
	if err := n.trust.ObserveOTP(ctx, id, stats.SMSVerification); err != nil {
		return false
	}
	return n.trust.Require(ctx, id, trustengine.ActionForwardOTP) == nil
}
