package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/updatecheck"
)

// fakeUpdater stands in for the updater client: Apply/Status to satisfy
// UpdateManager, plus the optional RunningImages and Check.
type fakeUpdater struct {
	images []updatecheck.RunningImage
	err    error
	check  updatecheck.Result
	delay  time.Duration
}

func (updater *fakeUpdater) Apply(context.Context, updatecheck.ApplyRequest) (updatecheck.Operation, error) {
	return updatecheck.Operation{}, nil
}

func (updater *fakeUpdater) Status(context.Context) (updatecheck.Operation, error) {
	return updatecheck.Operation{}, nil
}

func (updater *fakeUpdater) RunningImages(ctx context.Context) ([]updatecheck.RunningImage, error) {
	if updater.delay > 0 {
		select {
		case <-time.After(updater.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return updater.images, updater.err
}

func (updater *fakeUpdater) Check(context.Context) updatecheck.Result { return updater.check }

func TestTheReportCarriesWhatIsActuallyRunning(t *testing.T) {
	api, private, _ := licensedAPI(t)
	enabled := true
	updater := &fakeUpdater{
		images: []updatecheck.RunningImage{{
			Component: "api", Service: "api", Present: true,
			Reference: "nassimhub-beta-api:0.9.0",
			ImageID:   "sha256:" + strings.Repeat("1", 64),
			Digest:    "sha256:" + strings.Repeat("a", 64),
			Version:   "v1.16.0",
		}},
		check: updatecheck.Result{
			Status:        updatecheck.StatusUpdateAvailable,
			LatestVersion: "v1.17.0",
			AutoDownload:  &enabled,
			Download:      &updatecheck.Download{Version: "v1.17.0", State: updatecheck.DownloadFailed},
		},
	}
	api.updateManager = updater
	api.updateChecker = updater
	machineID := reportMachineID(t, api)

	reply := api.buildClientReport(context.Background(), signedReportRequest(t, machineID, private), machineID)
	if !reply.ImagesAvailable || len(reply.Images) != 1 || reply.Images[0].Digest != updater.images[0].Digest {
		t.Fatalf("images = %+v (available %v)", reply.Images, reply.ImagesAvailable)
	}
	if reply.Update.LatestVersion != "v1.17.0" || reply.Update.DownloadState != "failed" ||
		reply.Update.AutoDownload == nil || !*reply.Update.AutoDownload {
		t.Fatalf("update = %+v", reply.Update)
	}
}

// Without an updater the report says so, and says it as a fact of its own. An
// absent list would marshal as null and an empty one would read as "nothing
// is running", which are both wrong.
func TestWithoutAnUpdaterImagesAreUnavailableNotEmpty(t *testing.T) {
	api, private, _ := licensedAPI(t)
	api.updateManager = nil
	machineID := reportMachineID(t, api)
	reply := api.buildClientReport(context.Background(), signedReportRequest(t, machineID, private), machineID)
	encoded, _ := json.Marshal(reply)
	if reply.ImagesAvailable || !strings.Contains(string(encoded), `"images":[]`) {
		t.Fatalf("report = %s", encoded)
	}
}

func TestAFailingUpdaterDoesNotBreakTheReport(t *testing.T) {
	api, private, _ := licensedAPI(t)
	api.updateManager = &fakeUpdater{err: errors.New("updater down")}
	machineID := reportMachineID(t, api)
	reply := api.buildClientReport(context.Background(), signedReportRequest(t, machineID, private), machineID)
	if reply.ImagesAvailable {
		t.Fatal("a failed lookup was reported as available")
	}
	if len(reply.Entitlements.Features) == 0 {
		t.Fatal("the rest of the report was lost with the updater")
	}
}

// The report is answered from the NKN receive loop. A hung updater must cost
// a bounded wait, not the loop.
func TestASlowUpdaterIsGivenUpOn(t *testing.T) {
	api, private, _ := licensedAPI(t)
	api.updateManager = &fakeUpdater{delay: time.Minute}
	machineID := reportMachineID(t, api)
	started := time.Now()
	reply := api.buildClientReport(context.Background(), signedReportRequest(t, machineID, private), machineID)
	if elapsed := time.Since(started); elapsed > clientReportLookupTimeout+2*time.Second {
		t.Fatalf("the report waited %s for the updater", elapsed)
	}
	if reply.ImagesAvailable {
		t.Fatal("a timed-out lookup was reported as available")
	}
}
