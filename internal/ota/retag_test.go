package ota

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/updatecheck"
)

// fakeDocker is a scripted docker CLI. It keeps just enough state -- which
// references resolve to which image IDs, which names are tagged to what -- for
// the retag path to be exercised end to end, and records every command so a
// test can assert what did and did NOT happen.
type fakeDocker struct {
	commands [][]string
	// local maps a reference (repo@digest, or name:tag) to an image ID.
	local map[string]string
	// pullable maps a pull reference to the image ID it produces.
	pullable map[string]string
	// containers maps a Compose service to "reference|imageID".
	containers map[string]string
	// repoDigests maps an image ID to its RepoDigests JSON.
	repoDigests map[string]string
	// composeErrors are returned by successive `docker compose` calls.
	composeErrors []error
	composeCalls  int
}

func newFakeDocker() *fakeDocker {
	return &fakeDocker{
		local:       map[string]string{},
		pullable:    map[string]string{},
		containers:  map[string]string{},
		repoDigests: map[string]string{},
	}
}

func (docker *fakeDocker) Run(_ context.Context, name string, arguments ...string) ([]byte, error) {
	command := append([]string{name}, arguments...)
	docker.commands = append(docker.commands, command)
	if name != "docker" || len(arguments) == 0 {
		return nil, errors.New("unexpected command")
	}
	switch {
	case arguments[0] == "compose":
		index := docker.composeCalls
		docker.composeCalls++
		if index < len(docker.composeErrors) {
			return nil, docker.composeErrors[index]
		}
		return nil, nil
	case arguments[0] == "pull":
		reference := arguments[len(arguments)-1]
		id, ok := docker.pullable[reference]
		if !ok {
			return nil, errors.New("pull failed: " + reference)
		}
		docker.local[reference] = id
		return []byte("pulled"), nil
	case arguments[0] == "tag":
		source, target := arguments[1], arguments[2]
		id := source
		if resolved, ok := docker.local[source]; ok {
			id = resolved
		}
		docker.local[target] = id
		return nil, nil
	case arguments[0] == "image" && arguments[1] == "inspect":
		format, reference := arguments[3], arguments[4]
		if format == "{{json .RepoDigests}}" {
			if value, ok := docker.repoDigests[reference]; ok {
				return []byte(value), nil
			}
			return []byte("[]"), nil
		}
		if id, ok := docker.local[reference]; ok {
			return []byte(id + "\n"), nil
		}
		return nil, errors.New("No such image: " + reference)
	case arguments[0] == "ps":
		for index, argument := range arguments {
			if strings.HasPrefix(argument, "label=com.docker.compose.service=") && index > 0 {
				service := strings.TrimPrefix(argument, "label=com.docker.compose.service=")
				if _, ok := docker.containers[service]; ok {
					return []byte("container-" + service + "\n"), nil
				}
			}
		}
		return nil, nil
	case arguments[0] == "inspect":
		service := strings.TrimPrefix(arguments[len(arguments)-1], "container-")
		return []byte(docker.containers[service] + "\n"), nil
	case arguments[0] == "run":
		return nil, nil
	}
	return nil, errors.New("unscripted docker command: " + strings.Join(arguments, " "))
}

func (docker *fakeDocker) ran(prefix ...string) [][]string {
	var matched [][]string
	for _, command := range docker.commands {
		if len(command) >= len(prefix) && slices.Equal(command[:len(prefix)], prefix) {
			matched = append(matched, command)
		}
	}
	return matched
}

var fpkSlots = map[string]string{
	"api":      "nassimhub-beta-api:0.9.0",
	"web":      "nassimhub-beta-web:0.9.0",
	"hardware": "nassimhub-beta-hardware:0.9.0",
	"updater":  "nassimhub-beta-updater:0.9.0",
}

func newRetagRuntime(t *testing.T, docker *fakeDocker, mirrors ...string) (*DockerRuntime, string) {
	t.Helper()
	directory := t.TempDir()
	runtime, err := NewDockerRuntime(DockerRuntimeOptions{
		Runner:            docker,
		DeploymentDir:     directory,
		HostDeploymentDir: directory,
		StateDir:          filepath.Join(directory, "state"),
		StateVolume:       "nassimhub-beta-updater-state",
		SelfImage:         fpkSlots["updater"],
		Strategy:          StrategyRetag,
		ComposeProject:    "nassimhub-beta",
		ComposeFiles:      []string{"docker/docker-compose.yaml"},
		ComposeProfiles:   []string{"app"},
		Slots:             fpkSlots,
		Services:          map[string]string{"web": "web"},
		Mirrors:           mirrors,
		WorkerEnvironment: map[string]string{
			"MODEMDECK_UPDATE_STRATEGY": "retag",
			"MODEMDECK_COMPOSE_PROJECT": "nassimhub-beta",
		},
	})
	if err != nil {
		t.Fatalf("NewDockerRuntime() error = %v", err)
	}
	return runtime, directory
}

func apiTarget(digest string) Target {
	return Target{
		Name:      "api",
		Service:   "api",
		Image:     "ghcr.io/viper-boss/nassimhub",
		Version:   "v1.16.0",
		Digest:    digest,
		Reference: "ghcr.io/viper-boss/nassimhub:v1.16.0@" + digest,
		Changed:   true,
	}
}

func TestRetagInstallsByMovingTheSlotAndRecreatingTheService(t *testing.T) {
	docker := newFakeDocker()
	digest := testDigest("a")
	docker.local["ghcr.io/viper-boss/nassimhub@"+digest] = "sha256:new-api"
	docker.local[fpkSlots["api"]] = "sha256:old-api"
	runtime, _ := newRetagRuntime(t, docker)

	var progress []updatecheck.OperationComponentState
	err := runtime.Apply(context.Background(), Plan{
		Result:  updateResult("v1.16.0"),
		Targets: []Target{apiTarget(digest)},
	}, func(_ string, state updatecheck.OperationComponentState) error {
		progress = append(progress, state)
		return nil
	})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if docker.local[fpkSlots["api"]] != "sha256:new-api" {
		t.Fatalf("slot points at %q; want the new image", docker.local[fpkSlots["api"]])
	}
	if len(docker.ran("docker", "pull")) != 0 {
		t.Fatal("an image that was already downloaded was pulled again at install time")
	}
	compose := docker.ran("docker", "compose")
	if len(compose) != 1 {
		t.Fatalf("compose calls = %q", compose)
	}
	for _, expected := range [][]string{
		{"-p", "nassimhub-beta"},
		{"--profile", "app"},
		{"up", "--detach", "--no-build", "--no-deps"},
	} {
		if !containsSequence(compose[0], expected...) {
			t.Errorf("compose command missing %q: %q", expected, compose[0])
		}
	}
	if compose[0][len(compose[0])-1] != "api" {
		t.Fatalf("compose did not isolate the changed service: %q", compose[0])
	}
	if containsSequence(compose[0], "--env-file") {
		t.Fatal("named a .env the package does not have; compose fails on a missing --env-file")
	}
	want := []updatecheck.OperationComponentState{
		updatecheck.OperationComponentPulling,
		updatecheck.OperationComponentStaged,
		updatecheck.OperationComponentRestarting,
		updatecheck.OperationComponentReady,
	}
	if !slices.Equal(progress, want) {
		t.Fatalf("progress = %v; want %v", progress, want)
	}
}

func TestRetagRollsTheSlotBackToTheImageItHeldBefore(t *testing.T) {
	docker := newFakeDocker()
	digest := testDigest("a")
	docker.local["ghcr.io/viper-boss/nassimhub@"+digest] = "sha256:new-api"
	docker.local[fpkSlots["api"]] = "sha256:old-api"
	docker.composeErrors = []error{errors.New("api unhealthy"), nil}
	runtime, _ := newRetagRuntime(t, docker)

	err := runtime.Apply(context.Background(), Plan{
		Result:  updateResult("v1.16.0"),
		Targets: []Target{apiTarget(digest)},
	}, nil)
	var applyErr *ApplyError
	if !errors.As(err, &applyErr) || applyErr.RollbackFailed {
		t.Fatalf("Apply() error = %v; want a clean rollback", err)
	}
	// By ID: the old image's tag moved away, so only its ID still names it.
	if docker.local[fpkSlots["api"]] != "sha256:old-api" {
		t.Fatalf("slot after rollback = %q; want the old image back", docker.local[fpkSlots["api"]])
	}
	if docker.composeCalls != 2 {
		t.Fatalf("compose calls = %d; want the install and one rollback", docker.composeCalls)
	}
}

// A slot with nothing behind it before the install cannot be rolled back, and
// saying "rolled back" would be false.
func TestRetagSaysSoWhenThereWasNothingToRollBackTo(t *testing.T) {
	docker := newFakeDocker()
	digest := testDigest("a")
	docker.local["ghcr.io/viper-boss/nassimhub@"+digest] = "sha256:new-api"
	docker.composeErrors = []error{errors.New("api unhealthy")}
	runtime, _ := newRetagRuntime(t, docker)

	err := runtime.Apply(context.Background(), Plan{
		Result:  updateResult("v1.16.0"),
		Targets: []Target{apiTarget(digest)},
	}, nil)
	var applyErr *ApplyError
	if !errors.As(err, &applyErr) || !applyErr.RollbackFailed {
		t.Fatalf("Apply() error = %v; want RollbackFailed", err)
	}
}

func TestRetagRefusesToStartWithoutASlotForEveryComponent(t *testing.T) {
	directory := t.TempDir()
	slots := map[string]string{"api": "a:1", "web": "w:1", "hardware": "h:1"}
	_, err := NewDockerRuntime(DockerRuntimeOptions{
		Runner: newFakeDocker(), DeploymentDir: directory, HostDeploymentDir: directory,
		StateDir: filepath.Join(directory, "state"), StateVolume: "v", SelfImage: "u:1",
		Strategy: StrategyRetag, ComposeFiles: []string{"c.yaml"}, Slots: slots,
	})
	if err == nil || !strings.Contains(err.Error(), "updater") {
		t.Fatalf("error = %v; a missing slot must stop start-up, naming it", err)
	}
	slots["updater"] = "u@" + testDigest("f")
	if _, err := NewDockerRuntime(DockerRuntimeOptions{
		Runner: newFakeDocker(), DeploymentDir: directory, HostDeploymentDir: directory,
		StateDir: filepath.Join(directory, "state"), StateVolume: "v", SelfImage: "u:1",
		Strategy: StrategyRetag, ComposeFiles: []string{"c.yaml"}, Slots: slots,
	}); err == nil {
		t.Fatal("a digest reference was accepted as a slot; a slot is a name the Compose file uses")
	}
}

func TestPrefetchFallsBackToAMirrorAndRecordsWhatItVerified(t *testing.T) {
	docker := newFakeDocker()
	digest := testDigest("a")
	mirrored := "registry.cn-hangzhou.aliyuncs.com/viper-boss/nassimhub@" + digest
	docker.pullable[mirrored] = "sha256:new-api"
	runtime, _ := newRetagRuntime(t, docker, "registry.cn-hangzhou.aliyuncs.com/viper-boss/")

	if err := runtime.Prefetch(context.Background(), apiTarget(digest)); err != nil {
		t.Fatalf("Prefetch() error = %v", err)
	}
	pulls := docker.ran("docker", "pull")
	if len(pulls) != 2 ||
		pulls[0][2] != "ghcr.io/viper-boss/nassimhub@"+digest ||
		pulls[1][2] != mirrored {
		t.Fatalf("pulls = %q; want the canonical registry first, then the mirror, both by digest", pulls)
	}
	entry, ok := runtime.images.lookup("sha256:new-api")
	if !ok || entry.Digest != digest || entry.Version != "v1.16.0" {
		t.Fatalf("ledger = %+v, %v", entry, ok)
	}
	// And it touched nothing that runs.
	if len(docker.ran("docker", "tag")) != 0 || len(docker.ran("docker", "compose")) != 0 {
		t.Fatalf("prefetch changed what is running: %q", docker.commands)
	}
}

// A mirror serves only this project's images. Cloudflared belongs to its
// publisher, and the env strategy has no use for mirrors at all.
func TestMirrorsAreNeverAskedForSomebodyElsesImage(t *testing.T) {
	runtime, _ := newRetagRuntime(t, newFakeDocker(), "mirror.example/ns")
	if sources := runtime.sources("cloudflared"); len(sources) != 1 {
		t.Fatalf("cloudflared sources = %q", sources)
	}
	if sources := runtime.sources("api"); len(sources) != 2 || sources[1] != "mirror.example/ns/nassimhub" {
		t.Fatalf("api sources = %q", sources)
	}
}

func TestRunningReportsTheVerifiedDigestBehindASlot(t *testing.T) {
	docker := newFakeDocker()
	runtime, _ := newRetagRuntime(t, docker)
	docker.containers["api"] = fpkSlots["api"] + "|sha256:new-api"
	runtime.images.record("sha256:new-api", imageLedgerEntry{
		Component: "api", Digest: testDigest("a"), Version: "v1.16.0",
	})

	running, err := runtime.Running(context.Background(), "api", "api")
	if err != nil {
		t.Fatalf("Running() error = %v", err)
	}
	if !running.Present || running.Digest != testDigest("a") || running.Version != "v1.16.0" {
		t.Fatalf("running = %+v", running)
	}
}

// A slot's tag is a name, not a version. Reading "0.9.0" off
// nassimhub-beta-api:0.9.0 after it has been moved to v1.16.0 would report the
// wrong release with total confidence.
func TestASlotTagIsNotReportedAsAVersion(t *testing.T) {
	docker := newFakeDocker()
	runtime, _ := newRetagRuntime(t, docker)
	docker.containers["api"] = fpkSlots["api"] + "|sha256:loaded-from-tarball"

	running, _ := runtime.Running(context.Background(), "api", "api")
	if running.Version != "" || running.Digest != "" {
		t.Fatalf("running = %+v; a tarball-loaded image has no known version or digest", running)
	}
}

func TestRepoDigestsUnderAnUnrelatedNameAreNotBelieved(t *testing.T) {
	docker := newFakeDocker()
	runtime, _ := newRetagRuntime(t, docker)
	docker.containers["api"] = fpkSlots["api"] + "|sha256:x"
	docker.repoDigests["sha256:x"] = `["docker.io/someone/else@` + testDigest("e") + `"]`

	running, _ := runtime.Running(context.Background(), "api", "api")
	if running.Digest != "" {
		t.Fatalf("digest = %q; recorded under another repository, it says nothing about this release", running.Digest)
	}
	docker.repoDigests["sha256:x"] = `["ghcr.io/viper-boss/nassimhub@` + testDigest("a") + `"]`
	running, _ = runtime.Running(context.Background(), "api", "api")
	if running.Digest != testDigest("a") {
		t.Fatalf("digest = %q; the canonical repository's record should be used", running.Digest)
	}
}

func TestTheWorkerIsConfiguredLikeTheProcessThatStartedIt(t *testing.T) {
	docker := newFakeDocker()
	runtime, _ := newRetagRuntime(t, docker)
	if err := runtime.StartWorker(context.Background(), WorkerRequest{
		ExpectedVersion: "v1.16.0", Operation: operation("op-1"),
	}); err != nil {
		t.Fatalf("StartWorker() error = %v", err)
	}
	run := docker.ran("docker", "run")
	if len(run) != 1 ||
		!containsSequence(run[0], "--env", "MODEMDECK_UPDATE_STRATEGY=retag") ||
		!containsSequence(run[0], "--env", "MODEMDECK_COMPOSE_PROJECT=nassimhub-beta") {
		t.Fatalf("worker command = %q; without these it would plan the env strategy", run)
	}
	// The environment goes before the image; everything after the image is
	// the worker's own argument list.
	image := slices.Index(run[0], fpkSlots["updater"])
	if image < 0 || slices.Index(run[0], "MODEMDECK_UPDATE_STRATEGY=retag") > image {
		t.Fatalf("worker environment placed after the image: %q", run[0])
	}
}

func TestEnvStrategySkipsThePullForADownloadedImage(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte("MODEMDECK_VERSION=v1.9.2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	docker := newFakeDocker()
	digest := testDigest("a")
	docker.local["ghcr.io/viper-boss/nassimhub@"+digest] = "sha256:new-api"
	runtime := newDockerRuntimeForTest(t, docker, directory)
	target := apiTarget(digest)
	target.ImageEnv, target.RefEnv = "MODEMDECK_IMAGE", "MODEMDECK_API_IMAGE_REF"
	target.VersionEnv, target.DigestEnv = "MODEMDECK_API_VERSION", "MODEMDECK_API_DIGEST"

	if err := runtime.Apply(context.Background(), Plan{
		Result: updateResult("v1.16.0"), Targets: []Target{target},
	}, nil); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if pulls := docker.ran("docker", "pull"); len(pulls) != 0 {
		t.Fatalf("pulled a downloaded image again: %q", pulls)
	}
}
