package ota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/internal/updatecheck"
)

// Background downloads: fetch a release's images as soon as a signed release
// is seen, and never install anything.
//
// THE SPLIT IS THE FEATURE. An update used to be one action that took minutes:
// pull every image over whatever network the NAS has, then restart. On the
// networks these gateways sit on, the pull is the part that fails -- halfway,
// with the operator watching a ring that stopped moving. Pulling ahead of time
// moves that failure to a moment nobody is waiting for, lets it retry on its
// own, and turns "install" into a restart that takes seconds.
//
// WHAT IT NEVER DOES is change what is running. Prefetching puts image layers
// on disk; the running containers, the Compose file and the .env are not
// touched until the operator presses install. That line is enforced by the
// Prefetcher interface having no way to do anything else, and it is why this
// can be on by default: the worst it can cost is disk space.

// DownloadStore persists the state of the last background download.
type DownloadStore interface {
	Load() (*updatecheck.Download, error)
	Save(updatecheck.Download) error
}

// PreferenceStore persists the operator's update preferences.
type PreferenceStore interface {
	// Load never fails: an unreadable preference falls back to the default,
	// because refusing to report ANY update state over a corrupt settings file
	// would hide the updates the file was about.
	Load() updatecheck.Preferences
	Save(updatecheck.Preferences) error
}

// DefaultPreferences is what a fresh installation gets.
//
// AutoDownload is on by default, by explicit product decision: downloading is
// harmless (see the top of this file) and an operator who has to find a switch
// before updates become quick will mostly never find it.
func DefaultPreferences() updatecheck.Preferences {
	return updatecheck.Preferences{AutoDownload: true}
}

type FileDownloadStore struct{ path string }

func NewFileDownloadStore(directory string) *FileDownloadStore {
	return &FileDownloadStore{path: filepath.Join(directory, "download.json")}
}

func (store *FileDownloadStore) Load() (*updatecheck.Download, error) {
	contents, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read download state: %w", err)
	}
	var download updatecheck.Download
	if err := json.Unmarshal(contents, &download); err != nil {
		return nil, fmt.Errorf("decode download state: %w", err)
	}
	return &download, nil
}

func (store *FileDownloadStore) Save(download updatecheck.Download) error {
	return writeJSONFile(store.path, download)
}

type FilePreferenceStore struct{ path string }

func NewFilePreferenceStore(directory string) *FilePreferenceStore {
	return &FilePreferenceStore{path: filepath.Join(directory, "preferences.json")}
}

func (store *FilePreferenceStore) Load() updatecheck.Preferences {
	contents, err := os.ReadFile(store.path)
	if err != nil {
		return DefaultPreferences()
	}
	preferences := DefaultPreferences()
	if json.Unmarshal(contents, &preferences) != nil {
		return DefaultPreferences()
	}
	return preferences
}

func (store *FilePreferenceStore) Save(preferences updatecheck.Preferences) error {
	return writeJSONFile(store.path, preferences)
}

// writeJSONFile replaces a state file atomically, so a crash mid-write leaves
// the previous version rather than a truncated one.
func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create updater state directory: %w", err)
	}
	contents, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Base(path), err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	_ = temporary.Chmod(0o600)
	_, err = temporary.Write(contents)
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("commit %s: %w", filepath.Base(path), err)
	}
	return nil
}

// ErrDownloadUnsupported means the runtime cannot fetch ahead of installing.
var ErrDownloadUnsupported = errors.New("this deployment cannot download updates ahead of installing them")

// ErrNothingToDownload means there is no installable release on offer.
var ErrNothingToDownload = errors.New("no installable release is on offer")

// DownloaderOptions configure a Downloader.
type DownloaderOptions struct {
	Controller  *Controller
	Runtime     Runtime
	Downloads   DownloadStore
	Preferences PreferenceStore
	Logger      *slog.Logger
	// Interval between automatic checks. Jittered by up to a tenth either way,
	// so gateways installed on the same day do not all ask the mirrors, and
	// then the registry, in the same minute forever after.
	Interval time.Duration
	// InitialDelay lets the rest of the stack come up first. A gateway that
	// starts pulling hundreds of megabytes the moment it boots is competing
	// with its own modem initialisation for the one uplink.
	InitialDelay time.Duration
	Now          func() time.Time
}

// Downloader runs background downloads.
type Downloader struct {
	controller   *Controller
	prefetcher   Prefetcher
	downloads    DownloadStore
	preferences  PreferenceStore
	logger       *slog.Logger
	interval     time.Duration
	initialDelay time.Duration
	now          func() time.Time

	mu      sync.Mutex
	running bool
}

// NewDownloader builds a downloader, or reports that this runtime cannot
// prefetch.
func NewDownloader(options DownloaderOptions) (*Downloader, error) {
	if options.Controller == nil || options.Downloads == nil || options.Preferences == nil {
		return nil, errors.New("downloader needs a controller and its stores")
	}
	prefetcher, ok := options.Runtime.(Prefetcher)
	if !ok {
		return nil, ErrDownloadUnsupported
	}
	interval := options.Interval
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	delay := options.InitialDelay
	if delay < 0 {
		delay = 0
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Downloader{
		controller:   options.Controller,
		prefetcher:   prefetcher,
		downloads:    options.Downloads,
		preferences:  options.Preferences,
		logger:       logger,
		interval:     interval,
		initialDelay: delay,
		now:          now,
	}, nil
}

// Run checks and downloads on a schedule until ctx ends. Automatic runs honour
// the preference; the manual trigger (Start) does not.
func (downloader *Downloader) Run(ctx context.Context) {
	timer := time.NewTimer(downloader.initialDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if downloader.preferences.Load().AutoDownload {
			// A fresh check, not the cached one: this runs every few hours and
			// is the thing that notices a release was published.
			if _, err := downloader.run(updatecheck.WithRefresh(ctx)); err != nil &&
				!errors.Is(err, ErrNothingToDownload) && !errors.Is(err, ErrDownloadRunning) {
				downloader.logger.Warn("background update download did not complete", "error", err)
			}
		}
		jitter := time.Duration(rand.Int64N(int64(downloader.interval/5))) - downloader.interval/10
		timer.Reset(downloader.interval + jitter)
	}
}

// Start begins a download now, in the background, whatever the preference
// says: pressing "download" is a clearer instruction than a setting. Returns
// the state it started in, or the state of the download already running.
func (downloader *Downloader) Start(ctx context.Context) (updatecheck.Download, error) {
	plan, err := downloader.installablePlan(ctx)
	if err != nil {
		return updatecheck.Download{}, err
	}
	if !downloader.acquire() {
		if current, loadErr := downloader.downloads.Load(); loadErr == nil && current != nil {
			return *current, nil
		}
		return downloader.initialState(plan), nil
	}
	// Detached from the request: the HTTP call that asked for this returns
	// immediately, and a download is minutes long.
	go func() {
		defer downloader.release()
		if _, err := downloader.perform(context.WithoutCancel(ctx)); err != nil &&
			!errors.Is(err, ErrNothingToDownload) {
			downloader.logger.Warn("update download did not complete", "error", err)
		}
	}()
	return downloader.initialState(plan), nil
}

func (downloader *Downloader) acquire() bool {
	downloader.mu.Lock()
	defer downloader.mu.Unlock()
	if downloader.running {
		return false
	}
	downloader.running = true
	return true
}

func (downloader *Downloader) release() {
	downloader.mu.Lock()
	downloader.running = false
	downloader.mu.Unlock()
}

func (downloader *Downloader) installablePlan(ctx context.Context) (Plan, error) {
	plan, err := downloader.controller.Plan(ctx)
	if err != nil {
		return Plan{}, err
	}
	if !plan.Result.ApplyAvailable || plan.Result.LatestVersion == "" {
		return Plan{}, ErrNothingToDownload
	}
	return plan, nil
}

func (downloader *Downloader) initialState(plan Plan) updatecheck.Download {
	stamp := downloader.now().UTC().Format(time.RFC3339)
	download := updatecheck.Download{
		Version:   plan.Result.LatestVersion,
		State:     updatecheck.DownloadDownloading,
		StartedAt: stamp,
		UpdatedAt: stamp,
	}
	for _, target := range plan.Targets {
		if target.Changed {
			download.Components = append(download.Components, updatecheck.DownloadComponent{
				Name:  target.Name,
				State: updatecheck.DownloadPending,
			})
		}
	}
	return download
}

// ErrDownloadRunning means a download is already under way.
var ErrDownloadRunning = errors.New("a download is already running")

// run performs one download. Only one runs at a time: a second caller is told
// so rather than starting a parallel pull of the same layers.
func (downloader *Downloader) run(ctx context.Context) (updatecheck.Download, error) {
	if !downloader.acquire() {
		return updatecheck.Download{}, ErrDownloadRunning
	}
	defer downloader.release()
	return downloader.perform(ctx)
}

// perform is one download, with the running flag already held.
func (downloader *Downloader) perform(ctx context.Context) (updatecheck.Download, error) {
	// Never alongside an install. The install pulls whatever is not yet here
	// itself, so a download racing it can only add contention on the one
	// uplink the gateway has.
	if downloader.controller.OperationRunning() {
		return updatecheck.Download{}, ErrNothingToDownload
	}
	plan, err := downloader.installablePlan(ctx)
	if err != nil {
		return updatecheck.Download{}, err
	}

	// Already complete for this version: nothing to redo, and rewriting the
	// timestamps would make a finished download look fresh.
	if previous, loadErr := downloader.downloads.Load(); loadErr == nil && previous != nil &&
		previous.Version == plan.Result.LatestVersion && previous.State == updatecheck.DownloadReady &&
		downloader.allStaged(ctx, plan) {
		return *previous, nil
	}

	download := downloader.initialState(plan)
	if err := downloader.downloads.Save(download); err != nil {
		return download, err
	}

	failed := false
	for _, target := range plan.Targets {
		if !target.Changed {
			continue
		}
		state := updatecheck.DownloadReady
		staged, stagedErr := downloader.prefetcher.Staged(ctx, target)
		if stagedErr != nil || !staged {
			setDownloadComponent(&download, target.Name, updatecheck.DownloadDownloading)
			download.UpdatedAt = downloader.now().UTC().Format(time.RFC3339)
			_ = downloader.downloads.Save(download)
			if err := downloader.prefetcher.Prefetch(ctx, target); err != nil {
				downloader.logger.Warn("could not download an update image",
					"component", target.Name, "reference", target.Reference, "error", err)
				state = updatecheck.DownloadFailed
				failed = true
			}
		}
		setDownloadComponent(&download, target.Name, state)
		download.UpdatedAt = downloader.now().UTC().Format(time.RFC3339)
		if err := downloader.downloads.Save(download); err != nil {
			return download, err
		}
	}

	download.State = updatecheck.DownloadReady
	if failed {
		download.State = updatecheck.DownloadFailed
		download.ErrorCode = "download_failed"
	}
	download.UpdatedAt = downloader.now().UTC().Format(time.RFC3339)
	if err := downloader.downloads.Save(download); err != nil {
		return download, err
	}
	if failed {
		return download, errors.New("one or more update images could not be downloaded")
	}
	downloader.logger.Info("update downloaded and ready to install", "version", download.Version)
	return download, nil
}

func (downloader *Downloader) allStaged(ctx context.Context, plan Plan) bool {
	for _, target := range plan.Targets {
		if !target.Changed {
			continue
		}
		// A ready record is re-checked against the disk, because images can be
		// pruned underneath it -- by the operator, or by fnOS reclaiming space.
		if staged, err := downloader.prefetcher.Staged(ctx, target); err != nil || !staged {
			return false
		}
	}
	return true
}

func setDownloadComponent(download *updatecheck.Download, name string, state updatecheck.DownloadState) {
	for index := range download.Components {
		if download.Components[index].Name == name {
			download.Components[index].State = state
			return
		}
	}
}
