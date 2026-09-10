//go:build linux

package qdc507usb

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestHostInterfaceReleaseRunsScript verifies the happy path on Linux: a fixture
// script that exits 0 is invoked by the releaser without error.
func TestHostInterfaceReleaseRunsScript(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := filepath.Join(dir, "qdc507-interfaces.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fixture script: %v", err)
	}
	original := hostInterfaceReleaseScript
	defer func() { hostInterfaceReleaseScript = original }()
	hostInterfaceReleaseScript = script
	releaser := newHostInterfaceReleaser()
	if err := releaser.ReleaseHijackedInterfaces(context.Background()); err != nil {
		t.Fatalf("ReleaseHijackedInterfaces() error = %v, want nil", err)
	}
}
