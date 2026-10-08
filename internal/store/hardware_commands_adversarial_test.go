package store

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
)

// The single-effect guarantee for a message send or a call command is this
// insert: communication.Service calls BeginHardwareCommand before it talks to
// the modem and proceeds only when the row was CREATED by that call. The
// existing ledger test replays sequentially; a double click or a client retry
// arrives concurrently, so this one races it against the real database.
func TestAdversarialConcurrentDuplicateCommandIsClaimedExactlyOnce(t *testing.T) {
	t.Parallel()

	repository := newHardwareTestStore(t)
	ctx := context.Background()
	digest := bytes.Repeat([]byte{0x31}, 32)

	const submissions = 32
	var (
		wait    sync.WaitGroup
		mu      sync.Mutex
		created int
		pending int
		other   []string
	)
	start := make(chan struct{})
	for index := 0; index < submissions; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			command, wasCreated, err := repository.BeginHardwareCommand(ctx, "adv-request-1", "send_message", digest)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				other = append(other, err.Error())
			case wasCreated:
				created++
			case command.Status == HardwareCommandPending:
				pending++
			default:
				other = append(other, string(command.Status))
			}
		}()
	}
	close(start)
	wait.Wait()

	if len(other) != 0 {
		t.Fatalf("unexpected outcomes: %v", other)
	}
	if created != 1 || pending != submissions-1 {
		t.Fatalf("created = %d, pending = %d; exactly one of %d identical submissions may own the command",
			created, pending, submissions)
	}

	// The same request id with different content is a conflict, concurrently
	// too: a retry cannot be used to send a second, different message.
	var conflicts int
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, err := repository.BeginHardwareCommand(ctx, "adv-request-1", "send_message", bytes.Repeat([]byte{0x32}, 32))
			if errors.Is(err, ErrHardwareCommandConflict) {
				mu.Lock()
				conflicts++
				mu.Unlock()
			}
		}()
	}
	wait.Wait()
	if conflicts != 8 {
		t.Fatalf("%d of 8 altered replays were refused", conflicts)
	}

	// Once finished, a replay sees the completed command and its result, and
	// still does not get to run it again.
	if err := repository.FinishHardwareCommand(ctx, "adv-request-1", HardwareCommandCompleted, "adv-result-1", ""); err != nil {
		t.Fatalf("FinishHardwareCommand() error = %v", err)
	}
	command, wasCreated, err := repository.BeginHardwareCommand(ctx, "adv-request-1", "send_message", digest)
	if err != nil || wasCreated || command.Status != HardwareCommandCompleted {
		t.Fatalf("replay after completion = %+v, created = %v, error = %v", command, wasCreated, err)
	}
}
