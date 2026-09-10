package qdc507usb

import (
	"context"
	"errors"
	"os"
	"testing"
)

// TestHostInterfaceReleaseMissingScript must run on every platform: the production
// script path does not exist in a test environment, and the releaser must report a
// clear error wrapping os.ErrNotExist rather than silently succeeding.
func TestHostInterfaceReleaseMissingScript(t *testing.T) {
	t.Parallel()
	original := hostInterfaceReleaseScript
	defer func() { hostInterfaceReleaseScript = original }()
	hostInterfaceReleaseScript = "/nonexistent/qdc507-interfaces.sh"
	releaser := newHostInterfaceReleaser()
	err := releaser.ReleaseHijackedInterfaces(context.Background())
	if err == nil {
		t.Fatal("ReleaseHijackedInterfaces() error = nil, want an error for a missing script")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReleaseHijackedInterfaces() error = %v, want it to wrap os.ErrNotExist", err)
	}
}
