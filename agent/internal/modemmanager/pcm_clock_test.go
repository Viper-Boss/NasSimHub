package modemmanager

import (
	"context"
	"errors"
	"testing"
)

// The secondary PCM clock is the link in the call audio chain that nothing else
// reports on: with it off, AT+QPCMV? still says 1,2, the route mixers still
// read back on, the call still connects, and both directions are silence.
//
// The tests that matter most here are the ones about NOT writing. A guessed
// clock value is worse than an untouched clock, and the firmware string this
// was derived from is ambiguous about the value -- so the code has to act only
// on replies it genuinely understands.

func recorder(replies map[string]string, failures map[string]error) (
	func(context.Context, string) (string, error),
	*[]string,
) {
	sent := []string{}
	return func(_ context.Context, command string) (string, error) {
		sent = append(sent, command)
		if err, found := failures[command]; found {
			return "", err
		}
		return replies[command], nil
	}, &sent
}

func TestAClockAlreadyRunningIsLeftAlone(t *testing.T) {
	command, sent := recorder(map[string]string{
		quectelPCMClockQuery: "+QCFG: \"pcmclk\",1\r\n\r\nOK",
	}, nil)
	report := ensurePCMClock(context.Background(), command)
	if report.State != pcmClockOn {
		t.Fatalf("state = %q", report.State)
	}
	if report.Enabled {
		t.Fatal("a clock that was already on was reported as newly enabled")
	}
	for _, command := range *sent {
		if command == quectelPCMClockEnable {
			t.Fatal("a running clock was written to anyway")
		}
	}
}

func TestAClockReportedOffIsEnabledAndConfirmed(t *testing.T) {
	calls := 0
	command := func(_ context.Context, command string) (string, error) {
		if command == quectelPCMClockQuery {
			calls++
			// Off on the first read, on after the write.
			if calls == 1 {
				return "+QCFG: \"pcmclk\",0\r\nOK", nil
			}
			return "+QCFG: \"pcmclk\",1\r\nOK", nil
		}
		return "OK", nil
	}
	report := ensurePCMClock(context.Background(), command)
	if report.State != pcmClockOn || !report.Enabled {
		t.Fatalf("report = %+v", report)
	}
	if calls != 2 {
		t.Fatalf("the write was not confirmed by a read-back (%d queries)", calls)
	}
}

// The write has to be verified. This is a clock: if it does not actually come
// on, every other voice signal still looks healthy and the call is silent, so
// trusting an OK would restore exactly the invisibility this code removes.
func TestAClockThatDoesNotStayOnIsReportedRatherThanAssumed(t *testing.T) {
	command := func(_ context.Context, command string) (string, error) {
		if command == quectelPCMClockQuery {
			return "+QCFG: \"pcmclk\",0\r\nOK", nil
		}
		// The write is accepted and changes nothing.
		return "OK", nil
	}
	report := ensurePCMClock(context.Background(), command)
	if report.State == pcmClockOn {
		t.Fatal("a clock that never came on was reported as running")
	}
	if report.Reason == "" {
		t.Fatal("no reason was given for a clock that would not stay on")
	}
}

// The important negative case. An unrecognised reply means either the firmware
// has no such parameter or it answered in a shape this code does not know.
// Neither justifies writing a clock setting, and "off" would justify one.
func TestAnUnrecognisedReplyWritesNothing(t *testing.T) {
	for name, reply := range map[string]string{
		"bare OK":           "OK",
		"error":             "ERROR",
		"unsupported":       "+CME ERROR: 50",
		"another parameter": "+QCFG: \"ims\",1\r\nOK",
		"no value":          "+QCFG: \"pcmclk\"\r\nOK",
		"non-numeric value": "+QCFG: \"pcmclk\",\"auto\"\r\nOK",
		"empty":             "",
	} {
		command, sent := recorder(map[string]string{quectelPCMClockQuery: reply}, nil)
		report := ensurePCMClock(context.Background(), command)
		if report.State != pcmClockUnknown {
			t.Fatalf("%s: state = %q, want unknown", name, report.State)
		}
		for _, issued := range *sent {
			if issued == quectelPCMClockEnable {
				t.Fatalf("%s: a clock value was written on an unrecognised reply", name)
			}
		}
		if report.Reason == "" {
			t.Fatalf("%s: an unknown state carried no explanation", name)
		}
	}
}

func TestAQuotedValueIsStillRead(t *testing.T) {
	// Some firmware quotes the value. Treating that as unparseable would leave
	// a clock off on exactly the modules that need it switched on.
	command, _ := recorder(map[string]string{
		quectelPCMClockQuery: "+QCFG: \"pcmclk\",\"0\"\r\nOK",
	}, nil)
	state, _ := parsePCMClock("+QCFG: \"pcmclk\",\"0\"\r\nOK")
	if state != pcmClockOff {
		t.Fatalf("a quoted zero read as %q", state)
	}
	report := ensurePCMClock(context.Background(), command)
	if report.State == pcmClockUnknown {
		t.Fatal("a quoted value was treated as unrecognised")
	}
}

func TestANonZeroValueOtherThanOneCountsAsOn(t *testing.T) {
	// The firmware string this was derived from is ambiguous about which value
	// means what; all that is safe to conclude is that zero is off. A module
	// reporting 2 must not be rewritten to 1.
	command, sent := recorder(map[string]string{
		quectelPCMClockQuery: "+QCFG: \"pcmclk\",2\r\nOK",
	}, nil)
	report := ensurePCMClock(context.Background(), command)
	if report.State != pcmClockOn || report.Value != 2 {
		t.Fatalf("report = %+v", report)
	}
	for _, issued := range *sent {
		if issued == quectelPCMClockEnable {
			t.Fatal("a module reporting 2 was overwritten with 1")
		}
	}
}

func TestAFailedReadIsUnknownNotOff(t *testing.T) {
	command, sent := recorder(nil, map[string]error{
		quectelPCMClockQuery: errors.New("modem busy"),
	})
	report := ensurePCMClock(context.Background(), command)
	if report.State != pcmClockUnknown {
		t.Fatalf("a failed read reported %q", report.State)
	}
	if len(*sent) != 1 {
		t.Fatalf("commands after a failed read: %q", *sent)
	}
}
