package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/platform/database"
	"github.com/human-agent65535/modemdeck/internal/store"
	"github.com/human-agent65535/modemdeck/internal/trustengine"
)

type otpStatsFixture struct {
	counts    map[string]int
	err       error
	requested []string
}

func (s *otpStatsFixture) CommunicationStatsForNode(_ context.Context, _ time.Time, hours int, id string) (store.CommunicationStats, error) {
	s.requested = append(s.requested, id)
	if hours != 24 {
		return store.CommunicationStats{}, errors.New("wrong window")
	}
	return store.CommunicationStats{SMSVerification: s.counts[id]}, s.err
}

func TestOTPForwardingUsesFreshPerDeviceCountsAndEnforcement(t *testing.T) {
	for _, mode := range []trustengine.Enforcement{trustengine.Enforce, trustengine.Monitor} {
		t.Run(string(mode), func(t *testing.T) {
			ctx := context.Background()
			db, err := database.Open(ctx, database.Config{TargetPath: filepath.Join(t.TempDir(), "gate.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			storage, err := trustengine.NewSQLStore(db)
			if err != nil {
				t.Fatal(err)
			}
			_, key, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			engine, err := trustengine.New(ctx, trustengine.Options{Store: storage, Key: key, Enforcement: mode})
			if err != nil {
				t.Fatal(err)
			}
			if err := engine.Grandfather(ctx, map[string]string{"fixture-a": "a", "fixture-b": "b"}); err != nil {
				t.Fatal(err)
			}
			stats := &otpStatsFixture{counts: map[string]int{"fixture-a": 10, "fixture-b": 0}}
			nodes := &nassimhubNodes{trust: engine, stats: stats}
			a := store.Message{EndpointLineID: "nsh:fixture-a:0"}
			if !nodes.allowOTPForward(ctx, a) {
				t.Fatal("below-limit trusted device was blocked")
			}
			stats.counts["fixture-a"] = 11
			if got := nodes.allowOTPForward(ctx, a); got != (mode == trustengine.Monitor) {
				t.Fatal("fresh threshold was not applied before forwarding")
			}
			if !nodes.allowOTPForward(ctx, store.Message{EndpointLineID: "nsh:fixture-b:0"}) {
				t.Fatal("one device's count blocked another")
			}
			for _, endpoint := range []string{"nsh:fixture-a:1", "nsh:fixture-a:0:extra", "nsh::0"} {
				if nodes.allowOTPForward(ctx, store.Message{EndpointLineID: endpoint}) {
					t.Fatal("malformed node endpoint was forwarded")
				}
			}
			before := len(stats.requested)
			if !nodes.allowOTPForward(ctx, store.Message{EndpointLineID: "host-fixture"}) || len(stats.requested) != before {
				t.Fatal("host modem behavior changed")
			}
			if nodes.allowOTPForward(ctx, store.Message{EndpointMessageID: "nsh-in:fixture"}) {
				t.Fatal("node message missing endpoint must not bypass the gate")
			}
			stats.err = errors.New("database unavailable")
			if nodes.allowOTPForward(ctx, a) {
				t.Fatal("count failure must not disclose a verification code")
			}
		})
	}
}
