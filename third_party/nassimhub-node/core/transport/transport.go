// Package transport decides which link Core uses to reach a Node, and is the
// only place in Core that knows a Node has more than one address.
//
// The design rule everything here serves: a link is not an identity. A Node
// reached over USB and the same Node reached over Wi-Fi are one device with one
// device_id, and moving between links changes a display field and nothing else.
// Core must never key anything on an address, which is why the Selector hands
// out a base URL and a connection type but never lets a caller ask "which
// device is at this IP".
package transport

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/human-agent65535/nassimhub-node/proto"
)

// Endpoint is one way to reach a Node.
type Endpoint struct {
	// Type is the link class. It drives preference and is shown to the user.
	Type proto.ConnectionType
	// BaseURL is the root of the Node's API, for example http://10.0.0.7:7580.
	BaseURL string
}

// Priority orders links. USB is preferred because it is local, unmetered and
// unaffected by whatever is happening to the household Wi-Fi.
func (e Endpoint) Priority() int {
	switch e.Type {
	case proto.ConnectionUSB:
		return 0
	case proto.ConnectionWiFi:
		return 1
	default:
		return 2
	}
}

// ErrNoEndpoint reports that no configured link is currently usable.
var ErrNoEndpoint = errors.New("no usable endpoint for this node")

// Prober reports whether an endpoint is currently answering. It is injected so
// tests can drive failover deterministically instead of by unplugging cables.
type Prober func(ctx context.Context, endpoint Endpoint) error

// Selector picks the link for one Node.
//
// It is deliberately per-Node rather than global: two Nodes in one household
// can be on different links at the same time, and one of them losing USB must
// not move the other.
type Selector struct {
	mu        sync.Mutex
	deviceID  string
	endpoints []Endpoint
	probe     Prober
	now       func() time.Time

	active     *Endpoint
	lastSwitch time.Time
	// recheck is when the selector will next look for a higher-priority link
	// while a lower-priority one is working. Without it a Node that fell back
	// to Wi-Fi would stay there until something else forced a re-evaluation.
	recheck  time.Time
	interval time.Duration
	switches int
}

// Options configures a Selector.
type Options struct {
	DeviceID  string
	Endpoints []Endpoint
	Probe     Prober
	// UpgradeInterval is how often a working lower-priority link is re-checked
	// against a higher-priority one.
	UpgradeInterval time.Duration
	Now             func() time.Time
}

// DefaultUpgradeInterval balances noticing a reconnected USB cable quickly
// against probing a device that is busy carrying a call.
const DefaultUpgradeInterval = 15 * time.Second

// New builds a Selector.
func New(options Options) (*Selector, error) {
	if options.DeviceID == "" {
		return nil, errors.New("a selector requires the device id")
	}
	if options.Probe == nil {
		return nil, errors.New("a selector requires a prober")
	}
	interval := options.UpgradeInterval
	if interval <= 0 {
		interval = DefaultUpgradeInterval
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	selector := &Selector{
		deviceID: options.DeviceID,
		probe:    options.Probe,
		interval: interval,
		now:      now,
	}
	selector.SetEndpoints(options.Endpoints)
	return selector, nil
}

// DeviceID is the identity this selector serves. It never changes.
func (s *Selector) DeviceID() string { return s.deviceID }

// SetEndpoints replaces the known links.
//
// Endpoints are normalised and sorted by priority, and duplicates of the same
// type collapse, so repeated discovery of the same Node over the same link
// cannot grow the list without bound.
func (s *Selector) SetEndpoints(endpoints []Endpoint) {
	s.mu.Lock()
	defer s.mu.Unlock()

	unique := map[proto.ConnectionType]Endpoint{}
	for _, endpoint := range endpoints {
		endpoint.BaseURL = strings.TrimRight(strings.TrimSpace(endpoint.BaseURL), "/")
		if endpoint.BaseURL == "" || endpoint.Type == proto.ConnectionNone {
			continue
		}
		unique[endpoint.Type] = endpoint
	}
	s.endpoints = s.endpoints[:0]
	for _, endpoint := range unique {
		s.endpoints = append(s.endpoints, endpoint)
	}
	sort.Slice(s.endpoints, func(i, j int) bool {
		return s.endpoints[i].Priority() < s.endpoints[j].Priority()
	})

	// If the link we were using has disappeared from the configuration, drop
	// it so the next call re-selects rather than using a stale address.
	if s.active != nil {
		if _, still := unique[s.active.Type]; !still {
			s.active = nil
		} else {
			updated := unique[s.active.Type]
			s.active = &updated
		}
	}

	// The configuration changed, so any pending upgrade deadline is stale.
	// Clearing it makes the next Active re-evaluate immediately, which is what
	// a user expects after telling the NAS about a newly available link: the
	// switch should happen now, not at the end of a timer they cannot see.
	s.recheck = time.Time{}
}

// Endpoints reports the configured links in preference order.
func (s *Selector) Endpoints() []Endpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make([]Endpoint, len(s.endpoints))
	copy(copied, s.endpoints)
	return copied
}

// Active returns the endpoint Core should use right now.
//
// The rules, in order:
//
//  1. If nothing is selected, probe in preference order and take the first
//     that answers.
//  2. If the selected link stops answering, fall back to the next one that
//     does. This is the USB-unplugged case.
//  3. While a lower-priority link is in use, periodically re-probe the
//     higher-priority ones and move back when one returns. This is the
//     USB-replugged case.
//
// Every path returns an endpoint for the same device_id. There is no branch
// that can produce a second device.
func (s *Selector) Active(ctx context.Context) (Endpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.endpoints) == 0 {
		return Endpoint{}, fmt.Errorf("%w: %s has no configured endpoints", ErrNoEndpoint, s.deviceID)
	}

	if s.active != nil {
		current := *s.active
		if err := s.probe(ctx, current); err != nil {
			// The link we were on is gone. Fall through to a full re-selection
			// so a lower-priority link can take over.
			s.active = nil
		} else if s.now().Before(s.recheck) {
			return current, nil
		} else if better, ok := s.probeBetterLocked(ctx, current); ok {
			s.switchLocked(better)
			return better, nil
		} else {
			s.recheck = s.now().Add(s.interval)
			return current, nil
		}
	}

	var lastError error
	for _, endpoint := range s.endpoints {
		if err := s.probe(ctx, endpoint); err != nil {
			lastError = err
			continue
		}
		s.switchLocked(endpoint)
		return endpoint, nil
	}
	if lastError == nil {
		lastError = errors.New("every endpoint failed to answer")
	}
	return Endpoint{}, fmt.Errorf("%w: %s: %v", ErrNoEndpoint, s.deviceID, lastError)
}

// probeBetterLocked looks for a working link of higher priority than current.
func (s *Selector) probeBetterLocked(ctx context.Context, current Endpoint) (Endpoint, bool) {
	for _, endpoint := range s.endpoints {
		if endpoint.Priority() >= current.Priority() {
			// The list is sorted, so nothing after this point is better.
			break
		}
		if err := s.probe(ctx, endpoint); err == nil {
			return endpoint, true
		}
	}
	return Endpoint{}, false
}

func (s *Selector) switchLocked(endpoint Endpoint) {
	if s.active == nil || s.active.Type != endpoint.Type {
		s.switches++
		s.lastSwitch = s.now()
	}
	selected := endpoint
	s.active = &selected
	s.recheck = s.now().Add(s.interval)
}

// ConnectionType reports the link currently in use, without probing. It is what
// the NAS displays next to a Node, and it is the only thing that changes when a
// Node moves between USB and Wi-Fi.
func (s *Selector) ConnectionType() proto.ConnectionType {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return proto.ConnectionNone
	}
	return s.active.Type
}

// Switches counts how many times the link changed. It is diagnostic only: a
// Node that is flapping between USB and Wi-Fi is worth noticing, and the count
// makes that visible without inventing a second device to look at.
func (s *Selector) Switches() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.switches
}

// Invalidate forces the next Active call to re-select. Callers use it after a
// request fails at the network layer, so a dead link is abandoned immediately
// rather than after the next scheduled re-check.
func (s *Selector) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = nil
	s.recheck = time.Time{}
}
