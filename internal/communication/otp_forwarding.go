package communication

import (
	"context"
	"time"

	"github.com/human-agent65535/modemdeck/internal/smsclass"
	"github.com/human-agent65535/modemdeck/internal/store"
)

// SetOTPForwardGate controls external notifications only. Messages have already
// been committed to storage and the runtime revision before this gate runs.
// The gate must not log message bodies or verification codes.
func (s *Service) SetOTPForwardGate(gate func(context.Context, store.Message) bool) {
	s.otpGateMu.Lock()
	s.otpForwardGate = gate
	s.otpGateMu.Unlock()
}

func (s *Service) allowOTPForward(message store.Message) bool {
	if smsclass.Classify(message.Content) != smsclass.CategoryOTP {
		return true
	}
	s.otpGateMu.RLock()
	gate := s.otpForwardGate
	s.otpGateMu.RUnlock()
	if gate == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return gate(ctx, message)
}
