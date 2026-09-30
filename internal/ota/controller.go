package ota

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/internal/updatecheck"
)

var (
	ErrHardwareConfirmationRequired = errors.New("hardware update confirmation is required")
	ErrNoUpdate                     = errors.New("no applicable software update is available")
	ErrOperationRunning             = errors.New("a software update is already running")
	ErrVersionChanged               = errors.New("the requested release is no longer current")
	ErrNoOperation                  = errors.New("no software update operation has been recorded")
)

type ReleaseChecker interface {
	Check(context.Context) updatecheck.Result
}

type Runtime interface {
	// Running reports what one component's service is running. component is
	// the release name (api, web, ...), service the Compose service.
	Running(ctx context.Context, component, service string) (updatecheck.RunningImage, error)
	Managed(string) bool
	StartWorker(context.Context, WorkerRequest) error
	Apply(context.Context, Plan, ProgressReporter) error
}

// ServiceMapper is implemented by a runtime whose deployment names Compose
// services differently from the defaults below. Optional, because the
// upstream-style stack uses the defaults and its tests should not have to say
// so.
type ServiceMapper interface {
	ServiceFor(component, fallback string) string
}

// Prefetcher is implemented by a runtime that can fetch a release's images
// ahead of installing them. See Downloader.
type Prefetcher interface {
	// Staged reports whether the target's pinned image is already on this
	// machine.
	Staged(context.Context, Target) (bool, error)
	// Prefetch fetches it. It must never change what is running.
	Prefetch(context.Context, Target) error
}

type ProgressReporter func(string, updatecheck.OperationComponentState) error

type OperationStore interface {
	Load() (*updatecheck.Operation, error)
	Save(updatecheck.Operation) error
}

type Options struct {
	Checker    ReleaseChecker
	Manifests  ManifestLoader
	Runtime    Runtime
	Operations OperationStore
	// Downloads and Preferences are optional. Without them a check simply
	// carries no download state and no preference, which is how a deployment
	// without background downloads looks to the UI.
	Downloads   DownloadStore
	Preferences PreferenceStore
	Now         func() time.Time
}

type Controller struct {
	checker     ReleaseChecker
	manifests   ManifestLoader
	runtime     Runtime
	operations  OperationStore
	downloads   DownloadStore
	preferences PreferenceStore
	now         func() time.Time
	mu          sync.Mutex
}

type Target struct {
	Name       string
	Service    string
	Image      string
	Version    string
	Digest     string
	Reference  string
	ImageEnv   string
	RefEnv     string
	VersionEnv string
	DigestEnv  string
	CurrentRef string
	Current    string
	Changed    bool
}

type Plan struct {
	Result  updatecheck.Result
	Targets []Target
}

type WorkerRequest struct {
	ExpectedVersion string
	ConfirmHardware bool
	Operation       updatecheck.Operation
}

func New(options Options) (*Controller, error) {
	if options.Checker == nil || options.Manifests == nil ||
		options.Runtime == nil || options.Operations == nil {
		return nil, errors.New("complete OTA controller dependencies are required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Controller{
		checker:     options.Checker,
		manifests:   options.Manifests,
		runtime:     options.Runtime,
		operations:  options.Operations,
		downloads:   options.Downloads,
		preferences: options.Preferences,
		now:         now,
	}, nil
}

func (controller *Controller) Check(ctx context.Context) updatecheck.Result {
	plan, err := controller.plan(ctx)
	if err == nil {
		return controller.annotate(plan.Result)
	}
	result := controller.checker.Check(ctx)
	result.ApplyAvailable = false
	// A reason the checker already gave is kept: "this release needs a newer
	// installation first" is more use to an operator than the generic
	// "manifest unavailable" it would otherwise be flattened into.
	if result.ErrorCode == "" {
		if errors.Is(err, ErrManifestUnavailable) {
			result.ErrorCode = "ota_manifest_unavailable"
		} else {
			result.ErrorCode = "release_images_unavailable"
		}
	}
	if operation, loadErr := controller.operations.Load(); loadErr == nil {
		result.Operation = operation
	}
	return controller.annotate(result)
}

// annotate adds the operator's preference and, when it is for the version on
// offer, the download state.
//
// A download for any OTHER version is left out rather than shown: "ready" next
// to a newer release than the one downloaded would invite pressing install on
// the strength of work done for something else.
func (controller *Controller) annotate(result updatecheck.Result) updatecheck.Result {
	if operation, err := controller.operations.Load(); err == nil &&
		operation != nil && operation.State != updatecheck.OperationRunning {
		finished := *operation
		result.LastOperation = &finished
	}
	if controller.preferences != nil {
		preferences := controller.preferences.Load()
		enabled := preferences.AutoDownload
		result.AutoDownload = &enabled
	}
	if controller.downloads != nil && result.LatestVersion != "" {
		if download, err := controller.downloads.Load(); err == nil &&
			download != nil && download.Version == result.LatestVersion {
			result.Download = download
		}
	}
	return result
}

// Plan exposes the install plan to the downloader, which must fetch exactly
// what an install would -- no more, and by the same digests.
func (controller *Controller) Plan(ctx context.Context) (Plan, error) {
	return controller.plan(ctx)
}

// OperationRunning reports whether an install is in progress.
func (controller *Controller) OperationRunning() bool {
	operation, err := controller.operations.Load()
	return err == nil && operation != nil && operation.State == updatecheck.OperationRunning
}

func (controller *Controller) Apply(
	ctx context.Context,
	request updatecheck.ApplyRequest,
) (updatecheck.Operation, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()

	if operation, err := controller.operations.Load(); err == nil &&
		operation != nil && operation.State == updatecheck.OperationRunning {
		return updatecheck.Operation{}, ErrOperationRunning
	}
	plan, err := controller.plan(ctx)
	if err != nil {
		return updatecheck.Operation{}, err
	}
	if !plan.Result.ApplyAvailable {
		return updatecheck.Operation{}, ErrNoUpdate
	}
	if strings.TrimSpace(request.Version) != plan.Result.LatestVersion {
		return updatecheck.Operation{}, ErrVersionChanged
	}
	if plan.Result.HardwareConfirmationRequired && !request.ConfirmHardware {
		return updatecheck.Operation{}, ErrHardwareConfirmationRequired
	}
	operation := updatecheck.Operation{
		ID:            operationID(),
		State:         updatecheck.OperationRunning,
		TargetVersion: plan.Result.LatestVersion,
		StartedAt:     controller.now().UTC().Format(time.RFC3339),
		Components:    operationComponents(plan.Targets),
	}
	if err := controller.operations.Save(operation); err != nil {
		return updatecheck.Operation{}, err
	}
	if err := controller.runtime.StartWorker(ctx, WorkerRequest{
		ExpectedVersion: request.Version,
		ConfirmHardware: request.ConfirmHardware,
		Operation:       operation,
	}); err != nil {
		operation.State = updatecheck.OperationFailed
		operation.FinishedAt = controller.now().UTC().Format(time.RFC3339)
		operation.ErrorCode = "worker_start_failed"
		_ = controller.operations.Save(operation)
		return updatecheck.Operation{}, err
	}
	return operation, nil
}

func (controller *Controller) Status() (updatecheck.Operation, error) {
	operation, err := controller.operations.Load()
	if err != nil {
		return updatecheck.Operation{}, err
	}
	if operation == nil {
		return updatecheck.Operation{}, ErrNoOperation
	}
	return *operation, nil
}

func (controller *Controller) RunWorker(
	ctx context.Context,
	request WorkerRequest,
) error {
	plan, err := controller.plan(ctx)
	if err == nil && plan.Result.LatestVersion != request.ExpectedVersion {
		err = ErrVersionChanged
	}
	if err == nil && plan.Result.HardwareConfirmationRequired && !request.ConfirmHardware {
		err = ErrHardwareConfirmationRequired
	}
	if err == nil && !plan.Result.ApplyAvailable {
		err = ErrNoUpdate
	}
	operation := request.Operation
	if err == nil {
		operation.Components = operationComponents(plan.Targets)
		err = controller.operations.Save(operation)
	}
	if err == nil {
		err = controller.runtime.Apply(ctx, plan, func(
			name string,
			state updatecheck.OperationComponentState,
		) error {
			setOperationComponentState(&operation, name, state)
			return controller.operations.Save(operation)
		})
	}
	operation.FinishedAt = controller.now().UTC().Format(time.RFC3339)
	if err != nil {
		operation.State = updatecheck.OperationFailed
		operation.ErrorCode = applyErrorCode(err)
		for index := range operation.Components {
			if operation.Components[index].State != updatecheck.OperationComponentReady &&
				operation.Components[index].State != updatecheck.OperationComponentRolledBack {
				operation.Components[index].State = updatecheck.OperationComponentFailed
			}
		}
	} else {
		operation.State = updatecheck.OperationSucceeded
		operation.ErrorCode = ""
		for index := range operation.Components {
			operation.Components[index].State = updatecheck.OperationComponentReady
		}
	}
	if saveErr := controller.operations.Save(operation); saveErr != nil {
		return errors.Join(err, saveErr)
	}
	return err
}

func operationComponents(targets []Target) []updatecheck.OperationComponent {
	components := make([]updatecheck.OperationComponent, 0, len(targets))
	for _, target := range targets {
		if !target.Changed {
			continue
		}
		components = append(components, updatecheck.OperationComponent{
			Name:  target.Name,
			State: updatecheck.OperationComponentPending,
		})
	}
	return components
}

func setOperationComponentState(
	operation *updatecheck.Operation,
	name string,
	state updatecheck.OperationComponentState,
) {
	for index := range operation.Components {
		if operation.Components[index].Name == name {
			operation.Components[index].State = state
			return
		}
	}
}

func (controller *Controller) plan(ctx context.Context) (Plan, error) {
	result := controller.checker.Check(ctx)
	result.ApplyAvailable = false
	result.HardwareConfirmationRequired = false
	result.Components = nil
	if operation, err := controller.operations.Load(); err == nil {
		result.Operation = operation
	}
	if result.Status != updatecheck.StatusUpdateAvailable {
		return Plan{Result: result}, nil
	}
	manifest, err := controller.manifests.Load(ctx, result.LatestVersion)
	if err != nil {
		return Plan{}, err
	}
	// Only a signature-verified manifest may produce an install plan.
	//
	// This is the gate, and it is here rather than in the loader on purpose:
	// whichever loader is wired, and however the bytes arrived -- GitHub, a
	// peer over NKN, an offline file -- nothing can be installed unless the
	// manifest it came from was verified. Wiring an unsigned loader by mistake
	// then costs update availability, not integrity.
	if !manifest.Signed() {
		return Plan{}, fmt.Errorf(
			"%w: release manifest was not signature-verified", ErrManifestUnavailable)
	}

	targets := []Target{
		{Name: "api", Service: "api", Image: manifest.Images.API, Digest: manifest.Digests.API, Version: result.LatestVersion, ImageEnv: "MODEMDECK_IMAGE", RefEnv: "MODEMDECK_API_IMAGE_REF", VersionEnv: "MODEMDECK_API_VERSION", DigestEnv: "MODEMDECK_API_DIGEST"},
		{Name: "web", Service: "modemdeck", Image: manifest.Images.Web, Digest: manifest.Digests.Web, Version: result.LatestVersion, ImageEnv: "MODEMDECK_WEB_IMAGE", RefEnv: "MODEMDECK_WEB_IMAGE_REF", VersionEnv: "MODEMDECK_WEB_VERSION", DigestEnv: "MODEMDECK_WEB_DIGEST"},
		{Name: "hardware", Service: "hardware", Image: manifest.Images.Hardware, Digest: manifest.Digests.Hardware, Version: result.LatestVersion, ImageEnv: "MODEMDECK_HARDWARE_IMAGE", RefEnv: "MODEMDECK_HARDWARE_IMAGE_REF", VersionEnv: "MODEMDECK_HARDWARE_VERSION", DigestEnv: "MODEMDECK_HARDWARE_DIGEST"},
		{Name: "updater", Service: "updater", Image: manifest.Images.Updater, Digest: manifest.Digests.Updater, Version: result.LatestVersion, ImageEnv: "MODEMDECK_UPDATER_IMAGE", RefEnv: "MODEMDECK_UPDATER_IMAGE_REF", VersionEnv: "MODEMDECK_UPDATER_VERSION", DigestEnv: "MODEMDECK_UPDATER_DIGEST"},
	}
	// Belt and braces: the adapter already refuses a manifest missing any
	// first-party digest, so reaching this is a programming error rather than
	// bad input. It is checked anyway because the consequence of an empty
	// digest slipping through is the tag-resolution path coming back to life.
	for _, target := range targets {
		if !digestPattern.MatchString(target.Digest) {
			return Plan{}, fmt.Errorf(
				"%w: %s has no pinned digest", ErrManifestUnavailable, target.Name)
		}
	}
	// cloudflared only when the signed manifest actually names it. A release
	// that does not move cloudflared must leave it alone rather than fall
	// through to resolving it by tag, which is the path this change exists to
	// remove.
	if controller.runtime.Managed("cloudflared") && manifest.Cloudflared.Digest != "" {
		targets = append(targets, Target{
			Name:       "cloudflared",
			Service:    "cloudflared",
			Image:      manifest.Cloudflared.Image,
			Version:    manifest.Cloudflared.Version,
			Digest:     manifest.Cloudflared.Digest,
			VersionEnv: "MODEMDECK_CLOUDFLARED_VERSION",
			DigestEnv:  "MODEMDECK_CLOUDFLARED_DIGEST",
		})
	}
	// There is deliberately NO digest resolution step here any more.
	//
	// Every digest comes from the signed manifest, which is the only thing
	// that gets to say which bytes are this release. Asking the registry to
	// translate a mutable tag at install time was a second, unsigned answer to
	// the same question, and the registry could change it at any moment
	// without the manifest changing at all.

	mapper, _ := controller.runtime.(ServiceMapper)
	components := make([]updatecheck.Component, 0, len(targets))
	for index := range targets {
		target := &targets[index]
		if mapper != nil {
			target.Service = mapper.ServiceFor(target.Name, target.Service)
		}
		running, currentErr := controller.runtime.Running(ctx, target.Name, target.Service)
		if currentErr != nil {
			return Plan{}, currentErr
		}
		target.CurrentRef = running.Reference
		target.Current = running.Version
		if target.Current == "" {
			target.Current = strings.TrimSpace(result.CurrentVersion)
			if target.Current == "" {
				target.Current = "dev"
			}
		}
		target.Reference = target.Image + ":" + target.Version + "@" + target.Digest
		// An unknown running digest counts as changed. That is the case for an
		// image loaded from a tarball, which has no registry digest at all:
		// installing a release over it is the only way to learn what it is.
		target.Changed = !running.Present || running.Digest == "" || running.Digest != target.Digest
		if target.Name == "hardware" && target.Changed {
			result.HardwareConfirmationRequired = true
		}
		components = append(components, updatecheck.Component{
			Name:           target.Name,
			CurrentVersion: target.Current,
			TargetVersion:  target.Version,
			Changed:        target.Changed,
		})
	}
	result.Components = components
	result.ApplyAvailable = true
	return Plan{Result: result, Targets: targets}, nil
}

func imageDigest(reference string) string {
	if _, digest, ok := strings.Cut(reference, "@"); ok && digestPattern.MatchString(digest) {
		return digest
	}
	return ""
}

func imageVersion(reference string) string {
	name := reference
	if before, _, ok := strings.Cut(name, "@"); ok {
		name = before
	}
	slash := strings.LastIndexByte(name, '/')
	colon := strings.LastIndexByte(name, ':')
	if colon <= slash || colon == len(name)-1 {
		return ""
	}
	return name[colon+1:]
}

func operationID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return time.Now().UTC().Format("20060102T150405.000000000")
	}
	return hex.EncodeToString(buffer)
}

func applyErrorCode(err error) string {
	var applyErr *ApplyError
	if errors.As(err, &applyErr) && applyErr.RollbackFailed {
		return "apply_and_rollback_failed"
	}
	if errors.Is(err, ErrVersionChanged) {
		return "release_changed"
	}
	if errors.Is(err, ErrHardwareConfirmationRequired) {
		return "hardware_confirmation_required"
	}
	return "apply_failed"
}
