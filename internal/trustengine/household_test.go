package trustengine_test

import (
	"context"
	"errors"
	"github.com/human-agent65535/modemdeck/internal/trustengine"
	"testing"
	"time"
)

func household(f *fixture, ids ...string) {
	store, _ := trustengine.NewSQLStore(f.db)
	e, err := trustengine.New(context.Background(), trustengine.Options{Store: store, Key: f.key, Now: func() time.Time { return f.now }, HouseholdSIMDevices: ids})
	if err != nil {
		f.t.Fatal(err)
	}
	f.engine = e
}
func TestHouseholdOnlyKnownHealthySIMSwapsAreExempt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.engine.Grandfather(ctx, map[string]string{"home": "identity-of-home", "other": "identity-of-other"}); err != nil {
		t.Fatal(err)
	}
	f.observe("home", "sim1", true, time.Second)
	f.observe("other", "sim1", true, time.Second)
	f.observe("home", "sim2", true, time.Second)
	if f.subject("home").State != trustengine.Observation {
		t.Fatal("baseline should enter observation")
	}
	household(f, "home")
	f.observe("home", "sim2", true, time.Second)
	if f.subject("home").State != trustengine.Trusted {
		t.Fatal("known SIM-only observation was not approved")
	}
	f.observe("home", "sim3", true, time.Second)
	f.observe("other", "sim2", true, time.Second)
	if f.subject("home").State != trustengine.Trusted || f.subject("other").State != trustengine.Observation {
		t.Fatal("exception scope leaked")
	}
	if err := f.engine.Require(ctx, "home", trustengine.ActionDial); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Restrict(ctx, "home", "admin", "temporary", true); err != nil {
		t.Fatal(err)
	}
	f.observe("home", "sim4", true, time.Second)
	if f.subject("home").State != trustengine.Quarantine {
		t.Fatal("SIM setting bypassed quarantine")
	}
	household(f, "home")
	if err := f.engine.Require(ctx, "home", trustengine.ActionDial); err == nil {
		t.Fatal("restart bypassed quarantine")
	}
}
func TestHouseholdIdentityAndHardwareStillRestrict(t *testing.T) {
	for _, which := range []string{"identity", "hardware", "unhealthy"} {
		t.Run(which, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			f.engine.Grandfather(ctx, map[string]string{"home": "identity-of-home"})
			base := trustengine.Reading{SubjectID: "home", IdentityHash: "identity-of-home", SIMHash: "a", HardwareHash: "hardware-a", Healthy: true}
			if err := f.engine.Observe(ctx, base); err != nil {
				t.Fatal(err)
			}
			household(f, "home")
			base.SIMHash = "b"
			if which == "identity" {
				base.IdentityHash = "new"
			}
			if which == "hardware" {
				base.HardwareHash = "new"
			}
			if which == "unhealthy" {
				base.Healthy = false
			}
			if err := f.engine.Observe(ctx, base); err != nil {
				t.Fatal(err)
			}
			if f.subject("home").State == trustengine.Trusted {
				t.Fatal("unsafe exception")
			}
		})
	}
}
func TestIssuerAdministratorApprovalPersistsAndDoesNotMaskNewFailure(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.observe("device", "sim", true, time.Second)
	for _, state := range []string{"observation", "restricted", "quarantine"} {
		if state != "observation" {
			f.engine.Restrict(ctx, "device", "admin", "review", state == "quarantine")
		}
		if err := f.engine.AdministratorTrust(ctx, "device", "signed-console-admin", "owner verified"); err != nil {
			t.Fatal(err)
		}
		f.reopen()
		if err := f.engine.Require(ctx, "device", trustengine.ActionDial); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.engine.AdministratorTrust(ctx, "device", "admin", ""); !errors.Is(err, trustengine.ErrNotAllowed) {
		t.Fatal("empty reason accepted", err)
	}
	if err := f.engine.IntegrityFailure(ctx, "device", "new failure"); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Require(ctx, "device", trustengine.ActionDial); err == nil {
		t.Fatal("new integrity failure was ignored")
	}
	chain, err := f.engine.VerifyChain(ctx)
	if err != nil || !chain.OK {
		t.Fatal("audit chain invalid", err)
	}
}
