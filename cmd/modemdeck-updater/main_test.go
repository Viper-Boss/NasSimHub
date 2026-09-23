package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestTheCurrentVersionIsReadAfreshEachTime(t *testing.T) {
	t.Parallel()
	installedVersionPath := filepath.Join(t.TempDir(), "installed-version")
	current := currentVersionFunc(installedVersionPath, "v1.9.3")
	if got := current(); got != "v1.9.3" {
		t.Fatalf("before any install = %q; want the fallback", got)
	}
	if err := os.WriteFile(installedVersionPath, []byte("v1.9.5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := current(); got != "v1.9.5" {
		t.Fatalf("after an install = %q", got)
	}
	if err := os.WriteFile(installedVersionPath, []byte("v1.9.6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := current(); got != "v1.9.6" {
		t.Fatalf("after a second install = %q; a cached version would re-offer v1.9.6", got)
	}
}

func TestSlotAssignmentsAreStrict(t *testing.T) {
	t.Parallel()
	slots, err := parseAssignments("api=nassimhub-beta-api:0.9.0, web=nassimhub-beta-web:0.9.0")
	if err != nil || slots["web"] != "nassimhub-beta-web:0.9.0" || len(slots) != 2 {
		t.Fatalf("slots = %v, %v", slots, err)
	}
	for _, broken := range []string{"api", "api=", "=x:1", "api=a:1,api=b:1"} {
		if _, err := parseAssignments(broken); err == nil {
			t.Errorf("%q was accepted; a dropped slot installs three components of four", broken)
		}
	}
}

// Every setting buildController reads must reach the install worker, which
// rebuilds its configuration from its own environment. A setting that reaches
// the server but not the worker means the operator approves one plan and the
// worker installs another.
//
// The exceptions are the ones StartWorker passes itself, and the ones that
// only concern serving HTTP, which the worker never does.
func TestEveryPlanningSettingReachesTheWorker(t *testing.T) {
	t.Parallel()
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	passedByStartWorker := []string{
		"MODEMDECK_DEPLOYMENT_DIR",
		"MODEMDECK_HOST_DEPLOYMENT_DIR",
		"MODEMDECK_UPDATER_STATE_VOLUME",
		"MODEMDECK_CLOUDFLARE_ENABLED",
		"MODEMDECK_UPDATER_SELF_IMAGE",
	}
	serverOnly := []string{
		"MODEMDECK_UPDATER_TOKEN_FILE",
		"MODEMDECK_UPDATER_LISTEN_ADDRESS",
	}
	read := regexp.MustCompile(`(?:os\.Getenv|environmentOrDefault)\("(MODEMDECK_[A-Z0-9_]+)"`)
	for _, match := range read.FindAllStringSubmatch(string(source), -1) {
		name := match[1]
		if slices.Contains(passedByStartWorker, name) || slices.Contains(serverOnly, name) {
			continue
		}
		if !slices.Contains(workerEnvironmentKeys, name) {
			t.Errorf("%s is read by the updater but never reaches the install worker", name)
		}
	}
}

// The release planner decides which images to rebuild from which paths
// changed. The updater's list was written when it linked two internal
// packages; it now links five, and a change to one the list missed -- the
// signing key, the manifest format, the release channel -- would ship a
// release whose updater never received it, with nothing to say so.
func TestThePlannerRebuildsTheUpdaterForEveryPackageItLinks(t *testing.T) {
	output, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	planner, err := os.ReadFile(filepath.Join("..", "..", "deploy", "release-image-plan.sh"))
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "github.com/human-agent65535/modemdeck/internal/"
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		path := "internal/" + strings.TrimPrefix(line, prefix) + "/**"
		if !strings.Contains(string(planner), "':(glob)"+path+"'") {
			t.Errorf("the updater links %s but the planner does not rebuild it when %s changes", line, path)
		}
	}
}
