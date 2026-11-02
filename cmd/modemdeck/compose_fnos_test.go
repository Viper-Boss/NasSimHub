package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Review P1-8: the fnOS override used to make the api and web containers
// privileged to get past the trim kernel's storage sandbox. Measured on the
// NAS, root plus a handful of capabilities is enough, so neither service may
// become privileged again, and each keeps exactly the capabilities it was
// verified with. hardware stays privileged: it owns the modem devices.
func TestFnOSOverrideKeepsAPIAndWebUnprivileged(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(repositoryRoot(t), "deploy", "fnos", "compose.fnos.yml"))
	if err != nil {
		t.Fatal(err)
	}
	services := composeServiceBlocks(string(source))
	want := map[string][]string{
		"api":       {"DAC_OVERRIDE", "FOWNER"},
		"modemdeck": {"DAC_OVERRIDE", "CHOWN", "SETUID", "SETGID"},
	}
	for name, capabilities := range want {
		block, ok := services[name]
		if !ok {
			t.Fatalf("service %s is missing from compose.fnos.yml", name)
		}
		if regexp.MustCompile(`(?m)^\s+privileged:\s*true`).MatchString(block) {
			t.Errorf("%s is privileged again; add a capability instead (see the comment above it)", name)
		}
		var got []string
		for _, match := range regexp.MustCompile(`(?m)^\s+-\s+([A-Z_]+)\s*$`).FindAllStringSubmatch(block, -1) {
			got = append(got, match[1])
		}
		if strings.Join(got, ",") != strings.Join(capabilities, ",") {
			t.Errorf("%s cap_add = %v, want %v", name, got, capabilities)
		}
	}
}

// composeServiceBlocks splits the services: section into its two-space
// indented service entries.
func composeServiceBlocks(source string) map[string]string {
	blocks := map[string]string{}
	name := ""
	var current strings.Builder
	flush := func() {
		if name != "" {
			blocks[name] = current.String()
		}
		current.Reset()
	}
	for _, line := range strings.Split(source, "\n") {
		if match := regexp.MustCompile(`^  ([a-z0-9_-]+):\s*$`).FindStringSubmatch(line); match != nil {
			flush()
			name = match[1]
			continue
		}
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#") {
			flush()
			name = ""
			continue
		}
		current.WriteString(line + "\n")
	}
	flush()
	return blocks
}
