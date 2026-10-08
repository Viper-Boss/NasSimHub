package store

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
)

func TestNodePQPinIsWriteOnceUntilReset(t *testing.T) {
	t.Parallel()
	repository := newHardwareTestStore(t)
	ctx := context.Background()
	const node = "NSH-410-0A0B0C"

	if _, err := repository.NasSimHubNodePQPin(ctx, node); !errors.Is(err, ErrNasSimHubNodePQPinNotFound) {
		t.Fatalf("pin before any was stored: %v", err)
	}
	if _, err := repository.PinNasSimHubNodePQ(ctx, NasSimHubNodePQPin{NodeID: node, Algorithm: "ml-dsa-65", PublicKey: []byte{1}}); err == nil {
		t.Fatal("a pin was stored for a node that is not in the registry")
	}
	if err := repository.SaveNasSimHubNode(ctx, NasSimHubNodeRecord{
		ID: node, PublicKey: []byte{9}, PairState: NasSimHubPairStatePaired,
	}); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []NasSimHubNodePQPin{
		{NodeID: "", Algorithm: "ml-dsa-65", PublicKey: []byte{1}},
		{NodeID: node, Algorithm: "", PublicKey: []byte{1}},
		{NodeID: node, Algorithm: "ml-dsa-65"},
	} {
		if _, err := repository.PinNasSimHubNodePQ(ctx, invalid); err == nil {
			t.Fatalf("an incomplete pin was stored: %+v", invalid)
		}
	}

	created, err := repository.PinNasSimHubNodePQ(ctx, NasSimHubNodePQPin{NodeID: node, Algorithm: "ml-dsa-65", PublicKey: []byte{1, 2, 3}})
	if err != nil || !created {
		t.Fatalf("first pin: %v %v", created, err)
	}
	// A second key does not replace the first.
	created, err = repository.PinNasSimHubNodePQ(ctx, NasSimHubNodePQPin{NodeID: node, Algorithm: "ml-dsa-87", PublicKey: []byte{7, 7}})
	if err != nil || created {
		t.Fatalf("second pin: %v %v", created, err)
	}
	pin, err := repository.NasSimHubNodePQPin(ctx, node)
	if err != nil || pin.Algorithm != "ml-dsa-65" || !bytes.Equal(pin.PublicKey, []byte{1, 2, 3}) || pin.PinnedAt.IsZero() {
		t.Fatalf("stored pin: %+v %v", pin, err)
	}

	// Saving the node record again (a routine touch) keeps the pin.
	if err := repository.SaveNasSimHubNode(ctx, NasSimHubNodeRecord{
		ID: node, PublicKey: []byte{9}, PairState: NasSimHubPairStatePaired, DisplayName: "renamed",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.NasSimHubNodePQPin(ctx, node); err != nil {
		t.Fatalf("pin after the node record was saved again: %v", err)
	}

	removed, err := repository.ResetNasSimHubNodePQPin(ctx, node)
	if err != nil || !removed {
		t.Fatalf("reset: %v %v", removed, err)
	}
	if removed, _ := repository.ResetNasSimHubNodePQPin(ctx, node); removed {
		t.Fatal("a second reset reported a removal")
	}
	created, err = repository.PinNasSimHubNodePQ(ctx, NasSimHubNodePQPin{NodeID: node, Algorithm: "ml-dsa-87", PublicKey: []byte{7, 7}})
	if err != nil || !created {
		t.Fatalf("pin after reset: %v %v", created, err)
	}

	// Forgetting the node forgets its pin.
	if err := repository.DeleteNasSimHubNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.NasSimHubNodePQPin(ctx, node); !errors.Is(err, ErrNasSimHubNodePQPinNotFound) {
		t.Fatalf("pin after the node was deleted: %v", err)
	}
}

func TestConcurrentPinsAgreeOnOneKey(t *testing.T) {
	t.Parallel()
	repository := newHardwareTestStore(t)
	ctx := context.Background()
	const node = "NSH-410-0A0B0C"
	if err := repository.SaveNasSimHubNode(ctx, NasSimHubNodeRecord{
		ID: node, PublicKey: []byte{9}, PairState: NasSimHubPairStatePaired,
	}); err != nil {
		t.Fatal(err)
	}
	var (
		group   sync.WaitGroup
		mu      sync.Mutex
		winners int
	)
	for worker := 0; worker < 12; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			created, err := repository.PinNasSimHubNodePQ(ctx, NasSimHubNodePQPin{
				NodeID: node, Algorithm: "ml-dsa-65", PublicKey: []byte{byte(worker)},
			})
			if err != nil {
				t.Error(err)
				return
			}
			if created {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}(worker)
	}
	group.Wait()
	if winners != 1 {
		t.Fatalf("%d callers believe they created the pin", winners)
	}
}
