package store

import (
	"context"
	"errors"
	"github.com/human-agent65535/modemdeck/internal/auth"
	"testing"
	"time"
)

func TestOwnNumberSIMIsolationAndPersistence(t *testing.T) {
	s := newHardwareTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	a := HardwareLine{ID: "nsh:fixture-a", EquipmentIdentifier: "fixture-a", ICCID: "8900000000000000001", IMSI: "460110000000001", HomeCountryISO: "CN"}
	b := HardwareLine{ID: "nsh:fixture-b", EquipmentIdentifier: "fixture-b", ICCID: "8900000000000000002", IMSI: "460110000000002", HomeCountryISO: "CN"}
	apply := func(lines ...HardwareLine) {
		t.Helper()
		now = now.Add(time.Second)
		if err := s.ApplyHardwareSnapshot(ctx, HardwareSnapshot{BootEpoch: "fixture", Revision: now.String(), ObservedAt: now, Lines: lines}); err != nil {
			t.Fatal(err)
		}
	}
	apply(a, b)
	lines, err := s.Lines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	for _, line := range lines {
		if line.ICCID == a.ICCID {
			id = line.ID
		}
	}
	if id == "" {
		t.Fatal("missing attached SIM")
	}
	if _, err = s.UpdateOwnNumber(ctx, id, b.ICCID, "13800138000"); !errors.Is(err, ErrOwnNumberSIMChanged) {
		t.Fatal("wrong SIM accepted", err)
	}
	if _, err = s.UpdateOwnNumber(ctx, id, a.ICCID, "10000"); !errors.Is(err, ErrOwnNumberInvalid) {
		t.Fatal("short code accepted", err)
	}
	got, err := s.UpdateOwnNumber(ctx, id, a.ICCID, "13800138000")
	if err != nil || got != "+8613800138000" {
		t.Fatal(got, err)
	}
	apply(a, b)
	lines, err = s.Lines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range lines {
		if line.ICCID == a.ICCID && line.PhoneNumber != got {
			t.Fatal("number lost after poll")
		}
		if line.ICCID == b.ICCID && line.PhoneNumber != "" {
			t.Fatal("number leaked to second device")
		}
	}
	if _, err = s.UpdateOwnNumber(ctx, id, a.ICCID, ""); err != nil {
		t.Fatal(err)
	}
	apply(a, b)
	lines, _ = s.Lines(ctx)
	for _, line := range lines {
		if line.ICCID == a.ICCID && line.PhoneNumber != "" {
			t.Fatal("clear did not persist")
		}
	}
	if _, err = s.UpdateOwnNumber(ctx, id, a.ICCID, "13800138000"); err != nil {
		t.Fatal(err)
	}
	// The first device now contains a different SIM; the old form cannot save.
	a.ICCID = "8900000000000000003"
	a.IMSI = "460110000000003"
	apply(a, b)
	if _, err = s.UpdateOwnNumber(ctx, id, "8900000000000000001", "13900139000"); !errors.Is(err, ErrOwnNumberSIMChanged) {
		t.Fatal("detached SIM accepted", err)
	}
	lines, _ = s.Lines(ctx)
	for _, line := range lines {
		if line.ICCID == a.ICCID && line.PhoneNumber != "" {
			t.Fatal("old number inherited on SIM swap")
		}
	}
}

func TestOwnNumberMemberForbidden(t *testing.T) {
	s := newHardwareTestStore(t)
	ctx := auth.ContextWithPrincipal(context.Background(), auth.Principal{UserID: "fixture-member", Role: auth.RoleMember})
	if _, err := s.UpdateOwnNumber(ctx, "line", "sim", "13800138000"); !errors.Is(err, ErrOwnNumberForbidden) {
		t.Fatal(err)
	}
}
