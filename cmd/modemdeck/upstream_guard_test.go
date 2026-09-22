package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The update path must not reference the upstream project anywhere.
//
// It is checked mechanically rather than left to code review, deliberately.
//
// This repository is a derivative of human-agent65535/ModemDeck (see
// NOTICE.md). Upstream is a source of CODE. It is not a source of updates, and
// the difference is not cosmetic: the update path shipped pointing at
// upstream's release list and upstream's container images, so the first time an
// update actually ran, every gateway would have pulled upstream's build over
// this one. Device adapters, home-SIM features, the licensing scheme: replaced,
// by a routine update, with the project this was forked from.
//
// Nothing about those constants looked wrong. They matched the Go module path,
// they matched the image names, and each one individually read like the obvious
// value. That is exactly the kind of mistake a human reviewer restores while
// "fixing" something else, so it is checked mechanically.
//
// The one permitted occurrence is the Go module path itself,
// github.com/human-agent65535/modemdeck, which appears in every import line in
// the repository. That string is an identifier, not a network location --
// nothing fetches anything from it -- so renaming the module would touch every
// file in the tree to change something that cannot cause this bug. It is
// stripped before the check rather than whitelisted per-file, so a URL or an
// image reference that merely CONTAINS the module path is still caught.
func TestTheUpdatePathNamesNothingUpstream(t *testing.T) {
	root := repositoryRoot(t)

	const (
		// guardExemption marks a line that names upstream on purpose. Honoured
		// only in _test.go files; see the check below.
		guardExemption = "upstream-guard:allow"

		modulePath   = "github.com/human-agent65535/modemdeck"
		upstreamUser = "human-agent65535"
		upstreamRepo = "ModemDeck"
	)

	// Every file that decides where an update comes from or what it installs.
	paths := []string{
		filepath.Join("internal", "release"),
		filepath.Join("internal", "ota"),
		filepath.Join("internal", "updatecheck"),
		filepath.Join("cmd", "modemdeck-updater"),
		filepath.Join("cmd", "licensegen"),
	}

	checked := 0
	for _, directory := range paths {
		entries, err := os.ReadDir(filepath.Join(root, directory))
		if err != nil {
			t.Fatalf("read %s: %v", directory, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
				continue
			}
			name := filepath.Join(directory, entry.Name())
			contents, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			checked++

			for number, line := range strings.Split(string(contents), "\n") {
				// The module path is an identifier, not a location. Remove it
				// and see what is left.
				stripped := strings.ReplaceAll(line, modulePath, "")

				// A comment may discuss upstream -- several deliberately do,
				// because the reasoning is the point. Only code counts.
				if isCommentLine(line) {
					continue
				}
				// The owner name is unambiguous: nothing in this project
				// should mention it outside the module path. The bare repo
				// name is not -- "ModemDeck" also reads as a product name in
				// log lines and in GitHub API fixtures -- so it only counts
				// when it sits in something that addresses a host.
				addressesAHost := strings.Contains(stripped, "://") ||
					strings.Contains(stripped, "ghcr.io") ||
					strings.Contains(stripped, "github.com")
				// One deliberate exception, and only in test files: the test
				// that proves an upstream repository is REJECTED has to name
				// one. The marker is greppable, so the exception stays
				// visible, and it is ignored outside _test.go so production
				// code can never claim it.
				if strings.Contains(line, guardExemption) &&
					strings.HasSuffix(entry.Name(), "_test.go") {
					continue
				}
				if strings.Contains(stripped, upstreamUser) ||
					(strings.Contains(stripped, upstreamRepo) && addressesAHost) {
					t.Errorf(
						"%s:%d points the update path at the upstream project:\n\t%s\n"+
							"Upstream supplies code, not releases. An update that resolves there "+
							"installs upstream's build over this one.\n"+
							"Release images live in internal/release.Repositories.",
						name, number+1, strings.TrimSpace(line))
				}
			}
		}
	}

	// A guard that silently stops reading files stops guarding.
	if checked < 10 {
		t.Fatalf("only %d files were checked; this guard has lost sight of the update path", checked)
	}
}

// isCommentLine reports whether a line is entirely a comment.
//
// Deliberately conservative: it only skips lines that START with a comment
// marker, so `image := "ghcr.io/human-agent65535/x" // why` is still checked.
func isCommentLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") ||
		strings.HasPrefix(trimmed, "/*")
}
