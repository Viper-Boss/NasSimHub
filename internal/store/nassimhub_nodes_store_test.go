package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestNasSimHubNodesListsPersistedMetadataWithoutKeys(t *testing.T) {
	repository := newHardwareTestStore(t)
	_, err := repository.database.ExecContext(context.Background(), `
		INSERT INTO nassimhub_nodes
		(node_id, platform, display_name, public_key, pair_state, transport, usb_endpoint)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"NSH-test-1", "msm8916", "Test node", []byte("private-test-key"), "paired", "usb", "10.55.0.2:7580")
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := repository.NasSimHubNodes(context.Background())
	if err != nil || len(nodes) != 1 {
		t.Fatalf("NasSimHubNodes: len=%d err=%v", len(nodes), err)
	}
	if nodes[0].ID != "NSH-test-1" || nodes[0].PairState != "paired" {
		t.Fatalf("unexpected node summary: %+v", nodes[0])
	}
	encoded, err := json.Marshal(nodes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-test-key") || strings.Contains(string(encoded), "public_key") {
		t.Fatal("node listing exposed key material")
	}
}
