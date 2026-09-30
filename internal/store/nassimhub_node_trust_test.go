package store

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestNasSimHubNodeTrustRecordLifecycle(t *testing.T) {
	ctx := context.Background()
	repository := newHardwareTestStore(t)
	record := NasSimHubNodeRecord{
		ID:          "NSH-410-0A0B0C",
		Platform:    "msm8916",
		DisplayName: "客厅棒子",
		PublicKey:   bytes.Repeat([]byte{7}, 32),
		Certificate: []byte("certificate-der"),
		PairState:   NasSimHubPairStatePairing,
		Transport:   "usb",
		USBEndpoint: "https://10.55.0.2:7580",
	}
	if err := repository.SaveNasSimHubNode(ctx, record); err != nil {
		t.Fatal(err)
	}

	// Liveness is only recorded for a completed pairing.
	seen := time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC)
	if err := repository.TouchNasSimHubNode(ctx, record.ID, "wifi", record.USBEndpoint, "https://192.168.1.50:7580", seen); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.NasSimHubNodeRecord(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Transport != "usb" || stored.WiFiEndpoint != "" {
		t.Fatalf("a pairing-in-progress record was touched: %+v", stored)
	}

	record.PairState = NasSimHubPairStatePaired
	if err := repository.SaveNasSimHubNode(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := repository.TouchNasSimHubNode(ctx, record.ID, "wifi", record.USBEndpoint, "https://192.168.1.50:7580", seen); err != nil {
		t.Fatal(err)
	}
	stored, err = repository.NasSimHubNodeRecord(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.PairState != NasSimHubPairStatePaired || stored.Transport != "wifi" ||
		stored.WiFiEndpoint != "https://192.168.1.50:7580" || !bytes.Equal(stored.PublicKey, record.PublicKey) ||
		!bytes.Equal(stored.Certificate, record.Certificate) || stored.DisplayName != "客厅棒子" {
		t.Fatalf("stored record = %+v", stored)
	}
	if !stored.LastSeenAt.Equal(seen) {
		t.Fatalf("last seen = %v, want %v", stored.LastSeenAt, seen)
	}

	records, err := repository.NasSimHubNodeRecords(ctx)
	if err != nil || len(records) != 1 {
		t.Fatalf("records = %+v err=%v", records, err)
	}

	if err := repository.RecordNasSimHubNodeSIM(ctx, NasSimHubNodeSIM{
		NodeID: record.ID, SIMID: "89860000000000000001", ICCID: "89860000000000000001", Present: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordNasSimHubNodeSIM(ctx, NasSimHubNodeSIM{NodeID: record.ID}); err == nil {
		t.Fatal("a SIM without an identity must be refused")
	}

	if err := repository.DeleteNasSimHubNode(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.NasSimHubNodeRecord(ctx, record.ID); !errors.Is(err, ErrNasSimHubNodeNotFound) {
		t.Fatalf("deleted record lookup err = %v", err)
	}
	var sims int
	if err := repository.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM nassimhub_node_sims WHERE node_id = ?`, record.ID).Scan(&sims); err != nil {
		t.Fatal(err)
	}
	if sims != 0 {
		t.Fatalf("%d SIM rows survived the trust record", sims)
	}
}

func TestNasSimHubNodeRecordValidation(t *testing.T) {
	repository := newHardwareTestStore(t)
	for name, record := range map[string]NasSimHubNodeRecord{
		"no id":         {Platform: "msm8916", PublicKey: make([]byte, 32), PairState: NasSimHubPairStatePaired},
		"no key":        {ID: "N", Platform: "msm8916", PairState: NasSimHubPairStatePaired},
		"unknown state": {ID: "N", Platform: "msm8916", PublicKey: make([]byte, 32), PairState: "adopted"},
	} {
		if err := repository.SaveNasSimHubNode(context.Background(), record); err == nil {
			t.Errorf("%s: invalid record accepted", name)
		}
	}
}
