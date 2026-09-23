package ota

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/updatecheck"
)

// stagingRuntime is fakeRuntime plus a Prefetcher, recording what it was
// asked to do.
type stagingRuntime struct {
	fakeRuntime
	staged     map[string]bool
	failFetch  map[string]bool
	prefetched []string
	applied    bool
}

func (runtime *stagingRuntime) Staged(_ context.Context, target Target) (bool, error) {
	return runtime.staged[target.Name], nil
}

func (runtime *stagingRuntime) Prefetch(_ context.Context, target Target) error {
	runtime.prefetched = append(runtime.prefetched, target.Name)
	if runtime.failFetch[target.Name] {
		return errors.New("registry unreachable")
	}
	if runtime.staged == nil {
		runtime.staged = map[string]bool{}
	}
	runtime.staged[target.Name] = true
	return nil
}

func (runtime *stagingRuntime) Apply(context.Context, Plan, ProgressReporter) error {
	runtime.applied = true
	return nil
}

func newStagingRuntime() *stagingRuntime {
	return &stagingRuntime{fakeRuntime: fakeRuntime{images: map[string]string{
		"api":       "ghcr.io/viper-boss/nassimhub:v1.9.2@" + testDigest("1"),
		"modemdeck": "ghcr.io/viper-boss/nassimhub-web:v1.9.2@" + testDigest("b"),
		"hardware":  "ghcr.io/viper-boss/nassimhub-hardware:v1.9.2@" + testDigest("9"),
		"updater":   "ghcr.io/viper-boss/nassimhub-updater:v1.9.2@" + testDigest("c"),
	}}}
}

type downloadFixture struct {
	runtime     *stagingRuntime
	controller  *Controller
	downloader  *Downloader
	downloads   *FileDownloadStore
	preferences *FilePreferenceStore
	operations  *memoryOperationStore
}

func newDownloadFixture(t *testing.T) downloadFixture {
	t.Helper()
	directory := t.TempDir()
	runtime := newStagingRuntime()
	downloads := NewFileDownloadStore(directory)
	preferences := NewFilePreferenceStore(directory)
	operations := &memoryOperationStore{}
	manifest := testManifest(testDigest("8"))
	manifest.Digests.Hardware = testDigest("9")
	controller, err := New(Options{
		Checker: fakeChecker{result: updatecheck.Result{
			Status:         updatecheck.StatusUpdateAvailable,
			CurrentVersion: "v1.9.2",
			LatestVersion:  "v2.0.0",
		}},
		Manifests:   fakeManifestLoader{manifest: manifest},
		Runtime:     runtime,
		Operations:  operations,
		Downloads:   downloads,
		Preferences: preferences,
	})
	if err != nil {
		t.Fatal(err)
	}
	downloader, err := NewDownloader(DownloaderOptions{
		Controller: controller, Runtime: runtime,
		Downloads: downloads, Preferences: preferences,
		Now: func() time.Time { return time.Date(2026, 9, 23, 1, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return downloadFixture{runtime, controller, downloader, downloads, preferences, operations}
}

// The property that makes background downloads safe to have on by default.
func TestADownloadNeverInstallsAnything(t *testing.T) {
	fixture := newDownloadFixture(t)
	download, err := fixture.downloader.run(context.Background())
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if fixture.runtime.applied || fixture.runtime.started != nil {
		t.Fatal("a download applied an update or started the install worker")
	}
	if download.State != updatecheck.DownloadReady || download.Version != "v2.0.0" {
		t.Fatalf("download = %+v", download)
	}
	// Only api differs from what is running in this fixture, so only api is
	// fetched.
	if len(fixture.runtime.prefetched) != 1 || fixture.runtime.prefetched[0] != "api" {
		t.Fatalf("fetched %q; want only the changed api image", fixture.runtime.prefetched)
	}
}

func TestAutoDownloadIsOnUnlessTheOperatorTurnsItOff(t *testing.T) {
	fixture := newDownloadFixture(t)
	if !fixture.preferences.Load().AutoDownload {
		t.Fatal("a fresh installation does not download automatically")
	}
	result := fixture.controller.Check(context.Background())
	if result.AutoDownload == nil || !*result.AutoDownload {
		t.Fatalf("check result auto_download = %v", result.AutoDownload)
	}
	if err := fixture.preferences.Save(updatecheck.Preferences{AutoDownload: false}); err != nil {
		t.Fatal(err)
	}
	result = fixture.controller.Check(context.Background())
	if result.AutoDownload == nil || *result.AutoDownload {
		t.Fatal("switching auto-download off was not reported")
	}
}

// A corrupt preference file must not hide updates: it falls back to the
// default rather than failing every check.
func TestACorruptPreferenceFileFallsBackToTheDefault(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "preferences.json"), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !NewFilePreferenceStore(directory).Load().AutoDownload {
		t.Fatal("corrupt preferences switched auto-download off")
	}
}

func TestAFailedFetchIsReportedAndDoesNotClaimReady(t *testing.T) {
	fixture := newDownloadFixture(t)
	fixture.runtime.failFetch = map[string]bool{"api": true}
	download, err := fixture.downloader.run(context.Background())
	if err == nil {
		t.Fatal("a failed fetch reported success")
	}
	if download.State != updatecheck.DownloadFailed || download.ErrorCode != "download_failed" {
		t.Fatalf("download = %+v", download)
	}
	stored, _ := fixture.downloads.Load()
	if stored == nil || stored.State != updatecheck.DownloadFailed {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestTheCheckShowsTheDownloadOnlyForTheVersionOnOffer(t *testing.T) {
	fixture := newDownloadFixture(t)
	if _, err := fixture.downloader.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := fixture.controller.Check(context.Background())
	if result.Download == nil || result.Download.State != updatecheck.DownloadReady {
		t.Fatalf("download = %+v", result.Download)
	}
	// Now a download for some other version is on disk.
	if err := fixture.downloads.Save(updatecheck.Download{Version: "v1.99.0", State: updatecheck.DownloadReady}); err != nil {
		t.Fatal(err)
	}
	if result := fixture.controller.Check(context.Background()); result.Download != nil {
		t.Fatalf("a download for v1.99.0 was shown against v2.0.0: %+v", result.Download)
	}
}

func TestNoDownloadRunsWhileAnInstallDoes(t *testing.T) {
	fixture := newDownloadFixture(t)
	fixture.operations.operation = &updatecheck.Operation{ID: "x", State: updatecheck.OperationRunning}
	if _, err := fixture.downloader.run(context.Background()); !errors.Is(err, ErrNothingToDownload) {
		t.Fatalf("run() error = %v", err)
	}
	if len(fixture.runtime.prefetched) != 0 {
		t.Fatal("fetched during an install")
	}
}

func TestOnlyOneDownloadRunsAtATime(t *testing.T) {
	fixture := newDownloadFixture(t)
	if !fixture.downloader.acquire() {
		t.Fatal("could not take the flag")
	}
	defer fixture.downloader.release()
	if _, err := fixture.downloader.run(context.Background()); !errors.Is(err, ErrDownloadRunning) {
		t.Fatalf("run() error = %v; want ErrDownloadRunning", err)
	}
}

func TestARuntimeThatCannotPrefetchIsSaidToBeUnsupported(t *testing.T) {
	fixture := newDownloadFixture(t)
	_, err := NewDownloader(DownloaderOptions{
		Controller: fixture.controller, Runtime: &fakeRuntime{},
		Downloads: fixture.downloads, Preferences: fixture.preferences,
	})
	if !errors.Is(err, ErrDownloadUnsupported) {
		t.Fatalf("error = %v", err)
	}
}

func TestTheDownloadRoutesNeedTheTokenAndDoWhatTheySay(t *testing.T) {
	fixture := newDownloadFixture(t)
	handler, err := NewHandler(fixture.controller, "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	handler.WithDownloads(fixture.downloader, fixture.preferences)

	unauthorised := httptest.NewRecorder()
	handler.ServeHTTP(unauthorised, httptest.NewRequest(http.MethodPut, "/v1/updates/preferences",
		strings.NewReader(`{"auto_download":false}`)))
	if unauthorised.Code != http.StatusUnauthorized {
		t.Fatalf("status without token = %d", unauthorised.Code)
	}
	if !fixture.preferences.Load().AutoDownload {
		t.Fatal("an unauthenticated request changed the preference")
	}

	request := httptest.NewRequest(http.MethodPut, "/v1/updates/preferences",
		strings.NewReader(`{"auto_download":false}`))
	request.Header.Set("Authorization", "Bearer secret-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var saved updatecheck.Preferences
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &saved) != nil || saved.AutoDownload {
		t.Fatalf("PUT preferences = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPut, "/v1/updates/preferences",
		strings.NewReader(`{"auto_download":true,"install_automatically":true}`))
	request.Header.Set("Authorization", "Bearer secret-token")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("an unknown preference was accepted: %d", response.Code)
	}
}
