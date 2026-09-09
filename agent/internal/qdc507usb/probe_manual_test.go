//go:build linux

package qdc507usb

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestProbeModemCapabilities is a manual, hardware-gated probe. It is skipped
// unless QDC507_PROBE_PORT names a serial AT port. Read-only vendor queries
// only; it never changes modem configuration.
func TestProbeModemCapabilities(t *testing.T) {
	port := strings.TrimSpace(os.Getenv("QDC507_PROBE_PORT"))
	if port == "" {
		t.Skip("QDC507_PROBE_PORT not set")
	}
	commands := []string{
		"AT",
		"AT+CGMI",
		"AT+CGMM",
		"AT+CGMR",
		"AT+QCFG=\"ims\"",
		"AT+QCFG?",
		"AT+CEREG=4",
		"AT+CEREG?",
		"AT+QNWINFO",
		"AT+QTEMP?",
		"AT+CESQ",
		"AT+CGATT?",
		"AT+QENG=\"servingcell\"",
		"AT+VOWIFI?",
		"AT+QVOWIFI?",
		"AT+EPDG?",
		"AT+SPVOWIFI?",
		"AT+SPIMS?",
		"AT+CEER",
	}
	client := newSerialATClient()
	for _, command := range commands {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		response, err := client.Command(ctx, port, command, 3500*time.Millisecond)
		cancel()
		text := strings.TrimSpace(response)
		if len(text) > 400 {
			text = text[:400]
		}
		if err != nil {
			t.Logf("%-28s ERR  %v | %q", command, err, text)
			continue
		}
		t.Logf("%-28s OK   %q", command, text)
	}
}
