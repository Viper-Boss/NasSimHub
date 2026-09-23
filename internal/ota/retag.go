package ota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/internal/updatecheck"
)

// applyRetag installs a plan by moving image tags.
//
// THE ORDER, and why:
//
//  1. Every changed image is made local first (downloaded in the background
//     already, in the usual case). Nothing running is touched until all of
//     them are here, so a failed pull leaves the gateway exactly as it was.
//  2. The image each slot names NOW is written down, by image ID. That is the
//     rollback: an ID cannot be re-pointed, so it still names the old bytes
//     after the slot has moved.
//  3. The slots are moved to the new images, and the changed services are
//     recreated in one Compose call that waits for health.
//  4. If that fails, every slot is moved back to its recorded ID and the same
//     services are recreated again.
//
// A slot that did not exist before (a first install through this path, or an
// operator who pruned it) has no rollback target. That is reported as a failed
// rollback rather than hidden, because "rolled back" would then be false.
func (runtime *DockerRuntime) applyRetag(
	ctx context.Context,
	plan Plan,
	report ProgressReporter,
) error {
	type move struct {
		target   Target
		slot     string
		newID    string
		previous string
	}
	var moves []move
	for _, target := range plan.Targets {
		if !target.Changed {
			continue
		}
		slot := strings.TrimSpace(runtime.slots[target.Name])
		if slot == "" {
			// Unreachable for the four first-party components -- the
			// constructor insists on their slots -- so this is cloudflared, or a
			// component a future manifest adds. Refused rather than skipped: a
			// plan that silently installs three of four components is the
			// mixed-release state this whole path exists to prevent.
			return &ApplyError{Cause: fmt.Errorf("no image slot is configured for %s", target.Name)}
		}
		if err := reportProgress(report, target.Name, updatecheck.OperationComponentPulling); err != nil {
			return err
		}
		id, staged := runtime.stagedImageID(ctx, target)
		if !staged {
			if err := runtime.Prefetch(ctx, target); err != nil {
				_ = reportProgress(report, target.Name, updatecheck.OperationComponentFailed)
				return &ApplyError{Cause: err}
			}
			if id, staged = runtime.stagedImageID(ctx, target); !staged {
				_ = reportProgress(report, target.Name, updatecheck.OperationComponentFailed)
				return &ApplyError{Cause: fmt.Errorf("%s was pulled but cannot be found by its digest", target.Name)}
			}
		}
		runtime.images.record(id, imageLedgerEntry{
			Component: target.Name, Digest: target.Digest, Version: target.Version,
		})
		if err := reportProgress(report, target.Name, updatecheck.OperationComponentStaged); err != nil {
			return err
		}
		moves = append(moves, move{
			target:   target,
			slot:     slot,
			newID:    id,
			previous: runtime.imageIDOf(ctx, slot),
		})
	}

	if len(moves) == 0 {
		return runtime.recordInstalledVersion(plan.Result.LatestVersion)
	}

	if err := reportChangedTargets(plan.Targets, report, updatecheck.OperationComponentRestarting); err != nil {
		return err
	}
	var applyErr error
	for _, step := range moves {
		if _, err := runtime.runner.Run(ctx, "docker", "tag", step.newID, step.slot); err != nil {
			applyErr = fmt.Errorf("tag %s: %w", step.slot, err)
			break
		}
	}
	if applyErr == nil {
		applyErr = runtime.composeUp(ctx, nil, plan.Targets)
	}
	if applyErr == nil {
		if err := reportChangedTargets(plan.Targets, report, updatecheck.OperationComponentReady); err != nil {
			return err
		}
		return runtime.recordInstalledVersion(plan.Result.LatestVersion)
	}

	if err := reportChangedTargets(plan.Targets, report, updatecheck.OperationComponentRollingBack); err != nil {
		return errors.Join(&ApplyError{Cause: applyErr}, err)
	}
	var rollbackErr error
	for _, step := range moves {
		if step.previous == "" {
			rollbackErr = errors.Join(rollbackErr,
				fmt.Errorf("%s had no previous image to return to", step.slot))
			continue
		}
		if _, err := runtime.runner.Run(ctx, "docker", "tag", step.previous, step.slot); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore %s: %w", step.slot, err))
		}
	}
	if rollbackErr == nil {
		rollbackErr = runtime.composeUp(ctx, nil, plan.Targets)
	}
	if rollbackErr != nil {
		return &ApplyError{Cause: errors.Join(applyErr, rollbackErr), RollbackFailed: true}
	}
	if err := reportChangedTargets(plan.Targets, report, updatecheck.OperationComponentRolledBack); err != nil {
		return errors.Join(&ApplyError{Cause: applyErr}, err)
	}
	return &ApplyError{Cause: applyErr}
}

// imageIDOf reads the image ID a name currently points at, or "" when the
// name does not exist.
func (runtime *DockerRuntime) imageIDOf(ctx context.Context, name string) string {
	output, err := runtime.runner.Run(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// imageLedger remembers what this updater verified about images it pulled.
//
// Needed because a retagged image's name says nothing: the slot is called
// nassimhub-beta-api:0.9.0 whichever release is behind it. The ledger maps an
// image ID -- which does not change -- back to the digest and release it was
// pulled for, so "what is running" can still be answered by digest.
//
// Written by the updater (when it downloads) and by the one-shot worker (when
// it installs), which share the state volume. The two rarely overlap, and the
// worst a lost entry costs is one component being reported with no digest and
// re-planned as changed -- a redundant, harmless install -- so a read-merge-
// write with an atomic rename is the honest amount of locking.
type imageLedger struct {
	path string
	mu   sync.Mutex
}

type imageLedgerEntry struct {
	Component string `json:"component"`
	Digest    string `json:"digest"`
	Version   string `json:"version,omitempty"`
	At        int64  `json:"at"`
}

// ledgerLimit bounds the file. Old entries name images long since pruned.
const ledgerLimit = 32

func newImageLedger(path string) *imageLedger { return &imageLedger{path: path} }

func (ledger *imageLedger) read() map[string]imageLedgerEntry {
	entries := map[string]imageLedgerEntry{}
	contents, err := os.ReadFile(ledger.path)
	if err != nil {
		return entries
	}
	_ = json.Unmarshal(contents, &entries)
	return entries
}

func (ledger *imageLedger) lookup(imageID string) (imageLedgerEntry, bool) {
	if strings.TrimSpace(imageID) == "" {
		return imageLedgerEntry{}, false
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	entry, ok := ledger.read()[imageID]
	return entry, ok && digestPattern.MatchString(entry.Digest)
}

func (ledger *imageLedger) record(imageID string, entry imageLedgerEntry) {
	if strings.TrimSpace(imageID) == "" || !digestPattern.MatchString(entry.Digest) {
		return
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	entries := ledger.read()
	entry.At = time.Now().Unix()
	entries[imageID] = entry
	if len(entries) > ledgerLimit {
		ids := make([]string, 0, len(entries))
		for id := range entries {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(a, b int) bool { return entries[ids[a]].At > entries[ids[b]].At })
		for _, id := range ids[ledgerLimit:] {
			delete(entries, id)
		}
	}
	// Best effort, for the reason given on the type.
	_ = writeJSONFile(ledger.path, entries)
}

// RunningImages reports every managed component, for the client report.
func (runtime *DockerRuntime) RunningImages(ctx context.Context) ([]updatecheck.RunningImage, error) {
	defaults := map[string]string{
		"api": "api", "web": "modemdeck", "hardware": "hardware", "updater": "updater",
		"cloudflared": "cloudflared",
	}
	components := []string{"api", "web", "hardware", "updater"}
	if runtime.cloudflared {
		components = append(components, "cloudflared")
	}
	images := make([]updatecheck.RunningImage, 0, len(components))
	for _, component := range components {
		running, err := runtime.Running(ctx, component, runtime.ServiceFor(component, defaults[component]))
		if err != nil {
			return nil, err
		}
		images = append(images, running)
	}
	return images, nil
}
