// SPDX-License-Identifier: AGPL-3.0-only
package networkmanager

import (
	"context"
	"github.com/human-agent65535/nassimhub-node/proto"
	"strings"
	"time"
)

func (b *Backend) radioEnabled(ctx context.Context) *bool {
	value, err := b.run(ctx, "radio", "wifi")
	if err != nil {
		return nil
	}
	var enabled bool
	switch strings.TrimSpace(value) {
	case "enabled":
		enabled = true
	case "disabled":
		enabled = false
	default:
		return nil
	}
	return &enabled
}

// NetworkManager persists its Wi-Fi switch. No profile or USB setting changes.
func (b *Backend) SetRadioEnabled(ctx context.Context, enabled bool) (proto.WiFiStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if !b.lockOperation(ctx) {
		return proto.WiFiStatus{}, proto.Conflict("wifi.radio", "Wi-Fi is busy")
	}
	defer b.opMu.Unlock()
	b.mu.Lock()
	joining := b.state == proto.WiFiConnecting
	b.mu.Unlock()
	if joining {
		return proto.WiFiStatus{}, proto.Conflict("wifi.radio", "a connection attempt is in progress")
	}
	if b.radioEnabled(ctx) == nil {
		return proto.WiFiStatus{}, proto.Unavailable("wifi.radio", "Wi-Fi radio state is unavailable", nil)
	}
	value := "off"
	if enabled {
		value = "on"
	}
	if _, err := b.run(ctx, "radio", "wifi", value); err != nil {
		return proto.WiFiStatus{}, proto.Unavailable("wifi.radio", "could not change Wi-Fi radio power", err)
	}
	radio := b.radioEnabled(ctx)
	if radio == nil || *radio != enabled {
		return proto.WiFiStatus{}, proto.Unavailable("wifi.radio", "Wi-Fi radio change could not be verified", nil)
	}
	b.mu.Lock()
	b.state, b.target, b.failure, b.failedAt = "", "", "", time.Time{}
	b.mu.Unlock()
	return b.Status(ctx)
}
