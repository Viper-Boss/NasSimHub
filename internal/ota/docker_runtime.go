package ota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/human-agent65535/modemdeck/internal/release"
	"github.com/human-agent65535/modemdeck/internal/updatecheck"
)

const (
	workerContainerName        = "modemdeck-update-worker"
	WorkerSelfImageEnvironment = "MODEMDECK_UPDATER_WORKER_SELF_IMAGE"
	InstalledVersionFilename   = "installed-version"
)

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecCommandRunner struct{}

func (ExecCommandRunner) Run(
	ctx context.Context,
	name string,
	arguments ...string,
) ([]byte, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

// UpdateStrategy is how a new image takes the place of the running one.
type UpdateStrategy string

const (
	// StrategyEnv writes each image reference, pinned by digest, into the
	// deployment's .env and lets Compose interpolate it. The upstream-style
	// stack (docker-compose.yml + docker-compose.ota.yml) is built for this:
	// its services name ${MODEMDECK_*_IMAGE_REF}.
	StrategyEnv UpdateStrategy = "env"
	// StrategyRetag leaves the Compose file alone and moves its image TAGS
	// instead: the new image is pulled by digest and tagged with the name the
	// Compose file already uses, and the service is recreated.
	//
	// This exists for stacks whose Compose file names fixed local images and
	// was never meant to be edited in place -- the fnOS package, which loads
	// its images from tarballs and pins them with pull_policy: never, and the
	// NAS deployment in deploy/fnos. Neither can take a digest reference, and
	// neither needs to: the digest was verified when the image was pulled, and
	// a tag is just the name the stack looks the image up by.
	StrategyRetag UpdateStrategy = "retag"
)

type DockerRuntimeOptions struct {
	Runner            CommandRunner
	DeploymentDir     string
	HostDeploymentDir string
	StateDir          string
	StateVolume       string
	SelfImage         string
	Cloudflared       bool

	// Strategy defaults to StrategyEnv, which is what every deployment of
	// this runtime was before retag existed.
	Strategy UpdateStrategy
	// ComposeProject is the Compose project name, used both to find running
	// containers (by label) and to address the project. Default "modemdeck".
	ComposeProject string
	// ComposeFiles are relative to DeploymentDir. Empty means the env
	// strategy's own file selection; the retag strategy requires them.
	ComposeFiles []string
	// ComposeProfiles are passed as --profile. Empty means "ota" for the env
	// strategy and none for retag.
	ComposeProfiles []string
	// Slots maps component to the image name the Compose file uses for it.
	// Required by the retag strategy, for every component it manages.
	Slots map[string]string
	// Services maps component to Compose service where it differs from the
	// default (api->api, web->modemdeck, hardware->hardware, updater->updater).
	Services map[string]string
	// Mirrors are registry namespaces holding the same images as the
	// canonical one, tried in order after it. Retag strategy only.
	Mirrors []string
	// WorkerEnvironment is passed to the one-shot worker container, so it is
	// configured exactly as the process that started it.
	WorkerEnvironment map[string]string
}

type DockerRuntime struct {
	runner            CommandRunner
	deploymentDir     string
	hostDeploymentDir string
	stateDir          string
	stateVolume       string
	selfImage         string
	cloudflared       bool

	strategy          UpdateStrategy
	composeProject    string
	composeFiles      []string
	composeProfiles   []string
	slots             map[string]string
	services          map[string]string
	mirrors           []string
	workerEnvironment map[string]string
	images            *imageLedger
}

type ApplyError struct {
	Cause          error
	RollbackFailed bool
}

func (err *ApplyError) Error() string {
	message := "software update failed"
	if err.RollbackFailed {
		message = "software update and rollback failed"
	}
	if err.Cause == nil {
		return message
	}
	return message + ": " + err.Cause.Error()
}

func (err *ApplyError) Unwrap() error {
	return err.Cause
}

func NewDockerRuntime(options DockerRuntimeOptions) (*DockerRuntime, error) {
	runner := options.Runner
	if runner == nil {
		runner = ExecCommandRunner{}
	}
	for _, directory := range []string{options.DeploymentDir, options.HostDeploymentDir, options.StateDir} {
		if !filepath.IsAbs(directory) {
			return nil, errors.New("updater directories must be absolute")
		}
	}
	if strings.TrimSpace(options.StateVolume) == "" || strings.TrimSpace(options.SelfImage) == "" {
		return nil, errors.New("updater state volume and image are required")
	}
	strategy := options.Strategy
	if strategy == "" {
		strategy = StrategyEnv
	}
	project := strings.TrimSpace(options.ComposeProject)
	if project == "" {
		project = "modemdeck"
	}
	switch strategy {
	case StrategyEnv:
	case StrategyRetag:
		if len(options.ComposeFiles) == 0 {
			return nil, errors.New("the retag strategy needs the Compose files it manages")
		}
		// Every first-party component needs a slot. A missing one would be
		// planned, pulled, and then silently not installed -- a gateway
		// running two releases at once, with nothing on screen saying so.
		for _, component := range []string{"api", "web", "hardware", "updater"} {
			if strings.TrimSpace(options.Slots[component]) == "" {
				return nil, fmt.Errorf("the retag strategy needs an image slot for %s", component)
			}
		}
		for component, slot := range options.Slots {
			if strings.Contains(slot, "@") || strings.ContainsAny(slot, " \t\n") {
				return nil, fmt.Errorf("image slot for %s must be a plain name:tag, got %q", component, slot)
			}
		}
	default:
		return nil, fmt.Errorf("unknown update strategy %q", strategy)
	}
	for _, file := range options.ComposeFiles {
		if filepath.IsAbs(file) || strings.Contains(file, "..") {
			return nil, fmt.Errorf("Compose file %q must be relative to the deployment directory", file)
		}
	}
	return &DockerRuntime{
		runner:            runner,
		deploymentDir:     filepath.Clean(options.DeploymentDir),
		hostDeploymentDir: filepath.Clean(options.HostDeploymentDir),
		stateDir:          filepath.Clean(options.StateDir),
		stateVolume:       options.StateVolume,
		selfImage:         options.SelfImage,
		cloudflared:       options.Cloudflared,
		strategy:          strategy,
		composeProject:    project,
		composeFiles:      append([]string(nil), options.ComposeFiles...),
		composeProfiles:   append([]string(nil), options.ComposeProfiles...),
		slots:             copyMap(options.Slots),
		services:          copyMap(options.Services),
		mirrors:           append([]string(nil), options.Mirrors...),
		workerEnvironment: copyMap(options.WorkerEnvironment),
		images:            newImageLedger(filepath.Join(filepath.Clean(options.StateDir), "images.json")),
	}, nil
}

func copyMap(source map[string]string) map[string]string {
	copied := make(map[string]string, len(source))
	for key, value := range source {
		copied[key] = value
	}
	return copied
}

func (runtime *DockerRuntime) Managed(service string) bool {
	return service != "cloudflared" || runtime.cloudflared
}

// ServiceFor maps a component to its Compose service.
func (runtime *DockerRuntime) ServiceFor(component, fallback string) string {
	if service := strings.TrimSpace(runtime.services[component]); service != "" {
		return service
	}
	return fallback
}

// Running reports what a component's service is running.
func (runtime *DockerRuntime) Running(
	ctx context.Context,
	component, service string,
) (updatecheck.RunningImage, error) {
	running := updatecheck.RunningImage{Component: component, Service: service}
	output, err := runtime.runner.Run(
		ctx,
		"docker",
		"ps", "-aq",
		"--filter", "label=com.docker.compose.project="+runtime.composeProject,
		"--filter", "label=com.docker.compose.service="+service,
	)
	if err != nil {
		return running, err
	}
	containerID := strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
	if containerID == "" {
		return running, nil
	}
	output, err = runtime.runner.Run(
		ctx,
		"docker", "inspect", "--format", "{{.Config.Image}}|{{.Image}}", containerID,
	)
	if err != nil {
		return running, err
	}
	reference, imageID, _ := strings.Cut(strings.TrimSpace(string(output)), "|")
	running.Present = true
	running.Reference = strings.TrimSpace(reference)
	running.ImageID = strings.TrimSpace(imageID)

	// What the image IS, in order of how much each answer can be trusted:
	// a digest this updater verified when it pulled the image; a digest in
	// the reference Compose started it from; a digest the registry recorded
	// when the image was pulled by somebody else. A tarball-loaded image has
	// none of these, and is reported with no digest rather than a guess.
	if entry, ok := runtime.images.lookup(running.ImageID); ok {
		running.Digest = entry.Digest
		running.Version = entry.Version
	}
	if running.Digest == "" {
		running.Digest = imageDigest(running.Reference)
	}
	if running.Digest == "" && running.ImageID != "" {
		running.Digest = runtime.repoDigest(ctx, running.ImageID, component)
	}
	if running.Version == "" && runtime.strategy == StrategyEnv {
		// A retag slot's tag is a name, not a version -- the whole point of
		// the strategy is that it stays the same across releases -- so only
		// the env strategy's references say which version they are.
		running.Version = imageVersion(running.Reference)
	}
	return running, nil
}

// repoDigest reads the registry digest Docker recorded for an image, for the
// component's own repository or one of its mirrors only. An image can carry
// several; a digest recorded under some unrelated name says nothing about
// whether this is the release's image.
func (runtime *DockerRuntime) repoDigest(ctx context.Context, imageID, component string) string {
	output, err := runtime.runner.Run(
		ctx, "docker", "image", "inspect", "--format", "{{json .RepoDigests}}", imageID)
	if err != nil {
		return ""
	}
	var digests []string
	if json.Unmarshal(bytes.TrimSpace(output), &digests) != nil {
		return ""
	}
	accepted := map[string]bool{}
	for _, source := range runtime.sources(component) {
		accepted[source] = true
	}
	for _, entry := range digests {
		repository, digest, ok := strings.Cut(entry, "@")
		if ok && accepted[repository] && digestPattern.MatchString(digest) {
			return digest
		}
	}
	return ""
}

// sources lists where a component's image may be pulled from: its canonical
// repository first, then each mirror carrying the same image name.
func (runtime *DockerRuntime) sources(component string) []string {
	canonical := release.Repositories[component]
	if component == "cloudflared" {
		canonical = release.RepositoryCloudflared
	}
	if canonical == "" {
		return nil
	}
	list := []string{canonical}
	// Mirrors carry this project's own images only. Cloudflared belongs to its
	// publisher, and a mirror claiming to serve it is not one this project
	// runs.
	if component == "cloudflared" || runtime.strategy != StrategyRetag {
		return list
	}
	name := canonical[strings.LastIndexByte(canonical, '/')+1:]
	for _, mirror := range runtime.mirrors {
		mirror = strings.TrimRight(strings.TrimSpace(mirror), "/")
		if mirror != "" {
			list = append(list, mirror+"/"+name)
		}
	}
	return list
}

// Staged reports whether the target's image is on this machine, by digest.
func (runtime *DockerRuntime) Staged(ctx context.Context, target Target) (bool, error) {
	_, found := runtime.stagedImageID(ctx, target)
	return found, nil
}

// stagedImageID finds the target's image locally under any of its sources.
//
// Looked up as repository@digest, never by tag: a tag is whatever was pushed
// to it last, and the digest is the thing the signed manifest vouched for.
func (runtime *DockerRuntime) stagedImageID(ctx context.Context, target Target) (string, bool) {
	if !digestPattern.MatchString(target.Digest) {
		return "", false
	}
	for _, source := range runtime.sources(target.Name) {
		output, err := runtime.runner.Run(
			ctx, "docker", "image", "inspect", "--format", "{{.Id}}", source+"@"+target.Digest)
		if err != nil {
			continue
		}
		if id := strings.TrimSpace(string(output)); id != "" {
			return id, true
		}
	}
	return "", false
}

// Prefetch pulls the target's image by digest, trying mirrors in turn, and
// records what it pulled. It does not tag, restart or edit anything.
//
// Docker verifies the content against the digest in the reference, so a
// mirror can make a pull fail but cannot make it succeed with other bytes.
func (runtime *DockerRuntime) Prefetch(ctx context.Context, target Target) error {
	if !digestPattern.MatchString(target.Digest) {
		return fmt.Errorf("%s has no pinned digest", target.Name)
	}
	var failures []error
	for _, source := range runtime.sources(target.Name) {
		reference := source + "@" + target.Digest
		if _, err := runtime.runner.Run(ctx, "docker", "pull", reference); err != nil {
			failures = append(failures, err)
			continue
		}
		if id, ok := runtime.stagedImageID(ctx, target); ok {
			runtime.images.record(id, imageLedgerEntry{
				Component: target.Name,
				Digest:    target.Digest,
				Version:   target.Version,
			})
		}
		return nil
	}
	if len(failures) == 0 {
		return fmt.Errorf("%s has nowhere to be pulled from", target.Name)
	}
	return errors.Join(failures...)
}

func (runtime *DockerRuntime) StartWorker(
	ctx context.Context,
	request WorkerRequest,
) error {
	arguments := []string{
		"run", "--detach", "--rm",
		"--name", workerContainerName,
		"--mount", "type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock",
		"--mount", "type=bind,source=" + runtime.hostDeploymentDir + ",target=" + runtime.deploymentDir,
		"--mount", "type=volume,source=" + runtime.stateVolume + ",target=" + runtime.stateDir,
		"--read-only",
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=32m,mode=1777",
		"--cap-drop", "ALL",
		"--cap-add", "DAC_OVERRIDE",
		"--cap-add", "CHOWN",
		"--security-opt", "no-new-privileges:true",
		"--pids-limit", "128",
		"--env", "MODEMDECK_DEPLOYMENT_DIR=" + runtime.deploymentDir,
		"--env", "MODEMDECK_HOST_DEPLOYMENT_DIR=" + runtime.hostDeploymentDir,
		"--env", "MODEMDECK_UPDATER_STATE_VOLUME=" + runtime.stateVolume,
		"--env", WorkerSelfImageEnvironment + "=" + runtime.selfImage,
		"--env", "MODEMDECK_CLOUDFLARE_ENABLED=" + fmt.Sprintf("%t", runtime.cloudflared),
	}
	// The worker is a fresh process that rebuilds its configuration from the
	// environment. Every setting that shaped the plan the operator approved
	// has to reach it, or it would plan something else -- the env strategy by
	// default, against the wrong project -- and install that instead.
	keys := make([]string, 0, len(runtime.workerEnvironment))
	for key := range runtime.workerEnvironment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		arguments = append(arguments, "--env", key+"="+runtime.workerEnvironment[key])
	}
	arguments = append(arguments,
		runtime.selfImage,
		"worker",
		"--expected-version", request.ExpectedVersion,
		"--operation-id", request.Operation.ID,
		"--started-at", request.Operation.StartedAt,
	)
	if request.ConfirmHardware {
		arguments = append(arguments, "--confirm-hardware")
	}
	_, err := runtime.runner.Run(ctx, "docker", arguments...)
	return err
}

func (runtime *DockerRuntime) Apply(
	ctx context.Context,
	plan Plan,
	report ProgressReporter,
) error {
	if runtime.strategy == StrategyRetag {
		return runtime.applyRetag(ctx, plan, report)
	}
	for _, target := range plan.Targets {
		if !target.Changed {
			continue
		}
		if err := reportProgress(report, target.Name, updatecheck.OperationComponentPulling); err != nil {
			return err
		}
		// Already downloaded in the background: no second pull. This is what
		// makes an install after a download a restart rather than a transfer,
		// and what lets it succeed when the registry has since become
		// unreachable.
		if staged, _ := runtime.Staged(ctx, target); !staged {
			if _, err := runtime.runner.Run(ctx, "docker", "pull", target.Reference); err != nil {
				_ = reportProgress(report, target.Name, updatecheck.OperationComponentFailed)
				return &ApplyError{Cause: err}
			}
		}
		if err := reportProgress(report, target.Name, updatecheck.OperationComponentStaged); err != nil {
			return err
		}
	}

	environmentPath := filepath.Join(runtime.deploymentDir, ".env")
	previous, err := os.ReadFile(environmentPath)
	if err != nil {
		return &ApplyError{Cause: fmt.Errorf("read Compose environment: %w", err)}
	}
	values := map[string]string{"MODEMDECK_VERSION": plan.Result.LatestVersion}
	for _, target := range plan.Targets {
		if !target.Changed {
			continue
		}
		if target.ImageEnv != "" {
			values[target.ImageEnv] = target.Image
		}
		if target.RefEnv != "" {
			values[target.RefEnv] = target.Reference
		}
		values[target.VersionEnv] = target.Version
		values[target.DigestEnv] = target.Digest
		if target.Name == "updater" {
			values["MODEMDECK_UPDATER_SELF_IMAGE"] = target.Reference
		}
	}
	next, err := upsertEnvironment(previous, values)
	if err != nil {
		return &ApplyError{Cause: err}
	}
	if err := replaceFile(environmentPath, next); err != nil {
		return &ApplyError{Cause: err}
	}
	if err := reportChangedTargets(
		plan.Targets,
		report,
		updatecheck.OperationComponentRestarting,
	); err != nil {
		return err
	}
	if !hasChangedTarget(plan.Targets) {
		return runtime.recordInstalledVersion(plan.Result.LatestVersion)
	}
	if err := runtime.composeUp(ctx, next, plan.Targets); err == nil {
		if err := reportChangedTargets(
			plan.Targets,
			report,
			updatecheck.OperationComponentReady,
		); err != nil {
			return err
		}
		return runtime.recordInstalledVersion(plan.Result.LatestVersion)
	} else {
		applyErr := err
		if reportErr := reportChangedTargets(
			plan.Targets,
			report,
			updatecheck.OperationComponentRollingBack,
		); reportErr != nil {
			return errors.Join(&ApplyError{Cause: applyErr}, reportErr)
		}
		if restoreErr := replaceFile(environmentPath, previous); restoreErr != nil {
			return &ApplyError{Cause: errors.Join(applyErr, restoreErr), RollbackFailed: true}
		}
		if rollbackErr := runtime.composeUp(ctx, previous, plan.Targets); rollbackErr != nil {
			return &ApplyError{Cause: errors.Join(applyErr, rollbackErr), RollbackFailed: true}
		}
		if reportErr := reportChangedTargets(
			plan.Targets,
			report,
			updatecheck.OperationComponentRolledBack,
		); reportErr != nil {
			return errors.Join(&ApplyError{Cause: applyErr}, reportErr)
		}
		return &ApplyError{Cause: applyErr}
	}
}

func (runtime *DockerRuntime) recordInstalledVersion(version string) error {
	if err := os.MkdirAll(runtime.stateDir, 0o700); err != nil {
		return &ApplyError{Cause: fmt.Errorf("create updater state directory: %w", err)}
	}
	temporary, err := os.CreateTemp(runtime.stateDir, ".installed-version-*")
	if err != nil {
		return &ApplyError{Cause: fmt.Errorf("create installed version state: %w", err)}
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	// Tightening the mode is best effort: CreateTemp already made the file
	// 0600. The write result, however, must reach the outer err — assigning it
	// inside an "if err := ...;" block would drop it with the block scope and
	// commit a truncated state file.
	_ = temporary.Chmod(0o600)
	_, err = temporary.WriteString(strings.TrimSpace(version) + "\n")
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return &ApplyError{Cause: fmt.Errorf("write installed version state: %w", err)}
	}
	path := filepath.Join(runtime.stateDir, InstalledVersionFilename)
	if err := os.Rename(temporaryPath, path); err != nil {
		return &ApplyError{Cause: fmt.Errorf("commit installed version state: %w", err)}
	}
	return nil
}

func hasChangedTarget(targets []Target) bool {
	for _, target := range targets {
		if target.Changed {
			return true
		}
	}
	return false
}

func reportProgress(
	report ProgressReporter,
	name string,
	state updatecheck.OperationComponentState,
) error {
	if report == nil {
		return nil
	}
	return report(name, state)
}

func reportChangedTargets(
	targets []Target,
	report ProgressReporter,
	state updatecheck.OperationComponentState,
) error {
	for _, target := range targets {
		if !target.Changed {
			continue
		}
		if err := reportProgress(report, target.Name, state); err != nil {
			return err
		}
	}
	return nil
}

func (runtime *DockerRuntime) composeUp(
	ctx context.Context,
	environment []byte,
	targets []Target,
) error {
	values := parseEnvironment(environment)
	arguments := []string{
		"compose",
		"--project-directory", runtime.deploymentDir,
	}
	// The env strategy's .env always exists -- Apply has just rewritten it.
	// A retag deployment may have none (the fnOS package does not), and
	// naming a missing --env-file is an error, not an empty file.
	if runtime.strategy == StrategyEnv || fileExists(filepath.Join(runtime.deploymentDir, ".env")) {
		arguments = append(arguments, "--env-file", filepath.Join(runtime.deploymentDir, ".env"))
	}
	if len(runtime.composeFiles) > 0 {
		arguments = append(arguments, "-p", runtime.composeProject)
		for _, profile := range runtime.composeProfiles {
			arguments = append(arguments, "--profile", profile)
		}
		for _, file := range runtime.composeFiles {
			arguments = append(arguments, "-f", filepath.Join(runtime.deploymentDir, file))
		}
	} else {
		profiles := runtime.composeProfiles
		if len(profiles) == 0 {
			profiles = []string{"ota"}
		}
		for _, profile := range profiles {
			arguments = append(arguments, "--profile", profile)
		}
		arguments = append(arguments, "-f", filepath.Join(runtime.deploymentDir, "docker-compose.yml"))
		if values["MODEMDECK_HARDWARE_MODE"] == "advanced" {
			arguments = append(arguments, "-f", filepath.Join(runtime.deploymentDir, "docker-compose.advanced.yml"))
		}
		if values["MODEMDECK_CLOUDFLARE_ENABLED"] == "true" {
			arguments = append(arguments, "-f", filepath.Join(runtime.deploymentDir, "docker-compose.cloudflare.yml"))
			if values["MODEMDECK_CLOUDFLARE_TURN_KEY_ID"] != "" {
				arguments = append(arguments, "-f", filepath.Join(runtime.deploymentDir, "docker-compose.cloudflare-turn.yml"))
			}
		}
		arguments = append(arguments, "-f", filepath.Join(runtime.deploymentDir, "docker-compose.ota.yml"))
	}
	arguments = append(
		arguments,
		"up", "--detach", "--no-build", "--no-deps",
		"--wait", "--wait-timeout", "180",
	)
	for _, target := range targets {
		if target.Changed {
			arguments = append(arguments, target.Service)
		}
	}
	_, err := runtime.runner.Run(ctx, "docker", arguments...)
	return err
}

func upsertEnvironment(contents []byte, values map[string]string) ([]byte, error) {
	for key, value := range values {
		if strings.ContainsAny(key+value, "\r\n") || key == "" || strings.Contains(key, "=") {
			return nil, errors.New("invalid Compose environment value")
		}
	}
	lines := strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n")
	written := make(map[string]bool, len(values))
	for index, line := range lines {
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value, replace := values[key]
		if !replace {
			continue
		}
		if !written[key] {
			lines[index] = key + "=" + value
			written[key] = true
		} else {
			lines[index] = ""
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if !written[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		lines = append(lines, key+"="+values[key])
	}
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			result = append(result, line)
		}
	}
	return []byte(strings.Join(result, "\n") + "\n"), nil
}

func parseEnvironment(contents []byte) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(string(contents), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || strings.HasPrefix(key, "#") {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return values
}

func replaceFile(path string, contents []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect Compose environment: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".env-*")
	if err != nil {
		return fmt.Errorf("create Compose environment: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	// Restoring mode and owner is best effort — the updater may not be able to
	// chown — but the write result must reach the outer err. Assigning it to the
	// err scoped to an "if err := ...;" block would drop it and let a truncated
	// Compose environment be renamed over the real one.
	if err := temporary.Chmod(info.Mode().Perm()); err == nil {
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			_ = temporary.Chown(int(stat.Uid), int(stat.Gid))
		}
	}
	_, err = temporary.Write(contents)
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write Compose environment: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("commit Compose environment: %w", err)
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
