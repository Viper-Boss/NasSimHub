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

// ModuleShellRunner is an optional extension of the QDC507 voice runtime that
// runs shell commands on the module's own Linux system. System metrics use it
// to observe the module's CPU, memory and flash instead of the host's.
type ModuleShellRunner interface {
	ModuleShell(ctx context.Context, command string, timeout time.Duration) (string, error)
}

type QDC507USBProvisioner interface {
	Ensure(context.Context, domain.Line) (bool, error)
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
