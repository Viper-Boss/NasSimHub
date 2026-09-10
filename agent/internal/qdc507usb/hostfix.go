package qdc507usb

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// hostInterfaceReleaseScript is the host-side helper installed by the NAS image.
// It unbinds the kernel's option driver from the QDC507's non-serial interfaces
// (ADB, UAC) and lets their functional drivers (usbfs, snd-usb-audio) take over.
// It is guarded by flock internally, so repeated invocation is safe. It is a var
// (not a const) so tests can point it at a fixture script.
var hostInterfaceReleaseScript = "/usr/local/sbin/qdc507-interfaces.sh"

// InterfaceReleaser releases QDC507 USB interfaces that the kernel's option
// driver has wrongly claimed. It is a host-side repair: the agent cannot
// unbind a driver from inside its own container without asking the host.
type InterfaceReleaser interface {
	ReleaseHijackedInterfaces(context.Context) error
}

type hostInterfaceReleaser struct{}

func newHostInterfaceReleaser() InterfaceReleaser {
	return hostInterfaceReleaser{}
}

func (hostInterfaceReleaser) ReleaseHijackedInterfaces(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := os.Stat(hostInterfaceReleaseScript); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf(
				"QDC507 host interface release script %s is missing: %w",
				hostInterfaceReleaseScript,
				os.ErrNotExist,
			)
		}
		return fmt.Errorf(
			"QDC507 host interface release script %s is inaccessible: %w",
			hostInterfaceReleaseScript,
			err,
		)
	}
	cmd := exec.CommandContext(ctx, hostInterfaceReleaseScript)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return fmt.Errorf(
				"QDC507 host interface release script failed: %s: %w",
				message,
				err,
			)
		}
		return fmt.Errorf("QDC507 host interface release script failed: %w", err)
	}
	return nil
}
