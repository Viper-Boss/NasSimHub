package store

import (
	"context"
	"testing"
	"time"
)

// TestHardwareSnapshotEndpointIDChangePreservesStableLine reproduces the
// production bug where a QDC507 modem's agent-computed line ID (a SHA-256 hash
// over physical+equipment+device identifiers) changes after a USB interface
// re-bind, even though the SIM (ICCID/IMSI) is unchanged.
//
// Steps:
//  1. Apply a snapshot whose agent endpoint ID is "line_OLD" with a SIM that
//     creates a stable line L.
//  2. Apply a SECOND snapshot for the SAME SIM but with a DIFFERENT agent
//     endpoint ID "line_NEW" (the hash changed). equipment_identifier and
//     device_identifier stay present in both so the SIM still matches.
//  3. Assert that after the second snapshot, LineIDsByEndpoint maps "line_NEW"
//     to the SAME stable line L (exactly one mapping for the SIM).
//
// On the buggy code the legacy endpoint table is empty, so the new agent
// endpoint ID is never reconciled to the stable line and the mapping is lost.
func TestHardwareSnapshotEndpointIDChangePreservesStableLine(t *testing.T) {
	t.Parallel()

	repository := newHardwareTestStore(t)
	ctx := context.Background()
	observedOld := time.Date(2026, time.September, 1, 10, 0, 0, 0, time.UTC)
	observedNew := observedOld.Add(time.Hour)

	baseLine := HardwareLine{
		Model:               "QDC507",
		Firmware:            "fw-1",
		EquipmentIdentifier: "990000000000001",
		DeviceIdentifier:    "357000000000001",
		PrimaryPort:         "cdc-wdm0",
		State:               "registered",
		SignalKnown:         true,
		SignalQuality:       74,
		PhoneNumber:         "+8613800138000",
		ICCID:               "89861100000000000001",
		IMSI:                "460110000000005",
		Operator:            "China Telecom",
		HomeOperatorCode:    "46011",
		HomeCountryISO:      "CN",
	}

	// Snapshot 1: agent endpoint ID line_OLD.
	oldLine := baseLine
	oldLine.ID = "line_OLD"
	snapshotOld := HardwareSnapshot{
		BootEpoch:  "boot-1",
		Revision:   "snapshot-1",
		ObservedAt: observedOld,
		Lines:      []HardwareLine{oldLine},
	}
	resultOld, err := repository.ApplyHardwareSnapshotWithResult(ctx, snapshotOld)
	if err != nil {
		t.Fatalf("ApplyHardwareSnapshotWithResult(old) error = %v", err)
	}
	stableLineID := stableLineIDForICCID(t, repository, oldLine.ICCID)
	if got := resultOld.LineIDsByEndpoint["line_OLD"]; got != stableLineID {
		t.Fatalf("first snapshot LineIDsByEndpoint[line_OLD] = %q, want stable line %q",
			got, stableLineID)
	}

	// Snapshot 2: SAME SIM, DIFFERENT agent endpoint ID line_NEW.
	newLine := baseLine
	newLine.ID = "line_NEW"
	snapshotNew := HardwareSnapshot{
		BootEpoch:  "boot-1",
		Revision:   "snapshot-2",
		ObservedAt: observedNew,
		Lines:      []HardwareLine{newLine},
	}
	resultNew, err := repository.ApplyHardwareSnapshotWithResult(ctx, snapshotNew)
	if err != nil {
		t.Fatalf("ApplyHardwareSnapshotWithResult(new) error = %v", err)
	}

	// The stable line must be preserved across the agent endpoint ID change.
	if got := resultNew.LineIDsByEndpoint["line_NEW"]; got != stableLineID {
		t.Fatalf("after endpoint ID change, LineIDsByEndpoint[line_NEW] = %q, want stable line %q",
			got, stableLineID)
	}

	// And the new endpoint must supersede the old one (no orphaned mapping).
	if _, stillOld := resultNew.LineIDsByEndpoint["line_OLD"]; stillOld {
		t.Fatalf("stale endpoint mapping line_OLD still present after endpoint ID change: %+v",
			resultNew.LineIDsByEndpoint)
	}
}
