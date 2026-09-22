package provision

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MarkerName is the file that requests a factory reset on the next start.
//
// A MARKER AND A RESTART, rather than deleting things while the gateway runs.
// The data directory holds an open SQLite database, a TLS key the listener is
// using and a settings file the NKN client reads; deleting those underneath a
// running process produces a gateway that is half-reset and still serving,
// which is a worse state than either end of the operation. Writing one small
// file and exiting is atomic in the only sense that matters: either the marker
// is there on the next boot or it is not.
//
// The container restart policy brings the process back (`restart:
// unless-stopped`), and Reset runs before anything opens a file.
const MarkerName = ".reset-pending"

// Request is what the marker records.
//
// Kept for the log line after the reset, so the event is explainable
// afterwards: a gateway that wiped itself must be able to say why and on whose
// instruction, or the next person assumes data loss.
type Request struct {
	RequestedAt int64  `json:"requested_at"`
	Reason      string `json:"reason"`
	// StoredMachineID is the identity the wiped data belonged to.
	StoredMachineID string `json:"stored_machine_id,omitempty"`
	// CurrentMachineID is this machine.
	CurrentMachineID string `json:"current_machine_id,omitempty"`
}

// RequestReset writes the marker.
//
// Written with O_EXCL: two requests racing must not produce two half-written
// markers, and a marker that already exists means the reset is already pending,
// which is not an error.
func RequestReset(dataDirectory string, request Request) error {
	if strings.TrimSpace(dataDirectory) == "" {
		return fmt.Errorf("provision: no data directory")
	}
	request.RequestedAt = time.Now().Unix()
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	path := filepath.Join(dataDirectory, MarkerName)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return fmt.Errorf("provision: write reset marker: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(payload); err != nil {
		return err
	}
	// Flushed before the process exits, or a power cut between the request and
	// the restart loses the instruction and the gateway comes back still
	// carrying somebody else's identity.
	return file.Sync()
}

// ResetPending reports whether a reset is waiting, and what it recorded.
func ResetPending(dataDirectory string) (Request, bool) {
	if strings.TrimSpace(dataDirectory) == "" {
		return Request{}, false
	}
	raw, err := os.ReadFile(filepath.Join(dataDirectory, MarkerName))
	if err != nil {
		return Request{}, false
	}
	var request Request
	// A corrupt marker still means a reset was asked for. The contents are
	// only used for the log line, so losing them must not lose the
	// instruction -- the whole point is that this file's PRESENCE is the
	// decision.
	_ = json.Unmarshal(raw, &request)
	return request, true
}

// Reset empties the data directory.
//
// EVERYTHING GOES, except the marker itself, which is removed last. Deleting
// the whole directory rather than a list of known files is deliberate: a list
// is a thing that goes stale. Every feature added since -- a new integration's
// token file, a new cache, a new table -- lands in here, and a reset that
// enumerates paths would quietly start leaving the newest secrets behind,
// which is exactly the failure this is meant to prevent.
//
// The marker is removed only after the directory is empty, so a crash midway
// leaves it in place and the next boot finishes the job. The operation is
// therefore idempotent and interruptible, which matters because the most
// likely moment for a power cut is the one where somebody is reinstalling.
//
// What is NOT in here, and so survives: recordings (their own directory), the
// settings key file (a host secret, outside the data directory) and the
// container images. The settings key surviving is correct -- it encrypts
// nothing after this, because everything it encrypted is gone -- but it is
// also why the packaging checklist says a redistributed deployment needs a
// fresh one.
func Reset(dataDirectory string) error {
	if strings.TrimSpace(dataDirectory) == "" {
		return fmt.Errorf("provision: no data directory")
	}
	entries, err := os.ReadDir(dataDirectory)
	if err != nil {
		return fmt.Errorf("provision: read data directory: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == MarkerName {
			continue
		}
		path := filepath.Join(dataDirectory, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			// Reported rather than skipped: a reset that silently failed to
			// remove the database would hand the next owner an account and a
			// message history, having told everyone it was done.
			return fmt.Errorf("provision: remove %s: %w", entry.Name(), err)
		}
	}
	if err := os.Remove(filepath.Join(dataDirectory, MarkerName)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("provision: clear reset marker: %w", err)
	}
	return nil
}
