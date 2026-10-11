package communication

import (
	"errors"
	"github.com/human-agent65535/modemdeck/internal/agentclient"
	"testing"
)

func TestPermissionDenialPreservesActionableReason(t *testing.T) {
	err := translateAgentError("start call", &agentclient.OperationError{Code: "permission_denied", Message: "device is in observation"})
	if !errors.Is(err, ErrPermissionDenied) || errors.Is(err, ErrInternal) {
		t.Fatal("denial became internal error", err)
	}
	if err.Error() != "start call: device is in observation" {
		t.Fatal("lost reason", err)
	}
}
