package modemmanager

import (
	"context"
	"time"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

// DataPlane configures the kernel side of a connected ModemManager bearer.
// ModemManager remains the sole modem control plane; implementations must only
// mutate network state owned by the supplied stable line ID.
type DataPlane interface {
	Configure(context.Context, string, domain.DataConnection) error
	Release(context.Context, string) error
	OwnedLines() []string
}

type USBRecovery interface {
	Capability(string) domain.FeatureCapability
	Reset(context.Context, string) error
}

type QDC507VoiceRuntime interface {
	Status(domain.Line) domain.QDC507VoiceRuntimeStatus
}

// QDC507VoiceRouteObserver is an optional extension of the QDC507 voice runtime
// that wants to know when a line's calls begin and end.
//
// It exists because the module's PCM bridge establishes a route session that
// carries exactly ONE call: after the first one the DSP stops feeding the
// device and every subsequent call is silent in both directions, while the
// bridge process, its log, the USB audio enable flag and both PCM substreams
// all still report a healthy path. The runtime therefore has to restart the
// session between calls, and the call transitions are visible here rather than
// there. See agent/internal/qdc507voice/route_rearm.go.
//
// Optional so that a runtime which does not need it -- or a test fake -- is not
// forced to implement it.
type QDC507VoiceRouteObserver interface {
	ObserveCallActivity(line domain.Line, active bool)
}

// ModuleShellRunner is an optional extension of the QDC507 voice runtime that
// runs shell commands on the module's own Linux system. System metrics use it
// to observe the module's CPU, memory and flash instead of the host's.
type ModuleShellRunner interface {
	ModuleShell(ctx context.Context, command string, timeout time.Duration) (string, error)
}

type QDC507USBProvisioner interface {
	Ensure(context.Context, domain.Line) (bool, error)
	EnsureForce(context.Context, domain.Line) (bool, error)
}

type Options struct {
	DataPlane            DataPlane
	BearerStateFile      string
	RadioStateFile       string
	TrafficStateFile     string
	USBRecovery          USBRecovery
	QDC507VoiceRuntime   QDC507VoiceRuntime
	ModuleShell          ModuleShellRunner
	QDC507USBProvisioner QDC507USBProvisioner
}

type noopDataPlane struct{}

func (noopDataPlane) Configure(context.Context, string, domain.DataConnection) error {
	return nil
}

func (noopDataPlane) Release(context.Context, string) error {
	return nil
}

func (noopDataPlane) OwnedLines() []string {
	return nil
}
