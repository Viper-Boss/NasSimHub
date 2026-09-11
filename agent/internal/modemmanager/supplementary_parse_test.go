package modemmanager

import (
	"strings"
	"testing"
)

// Responses in this file are the shapes real modems return, including the
// awkward ones: multiple class lines, omitted optional fields, empty commas
// left in the middle, and CRLF line endings. A parser tested only against the
// tidy single-line case passes and then misreads the first real network it
// meets.

func TestForwardingIsReadFromTheVoiceClassOnly(t *testing.T) {
	// Three classes, and only voice is forwarded. Reading the wrong line would
	// tell the operator their calls go somewhere they do not.
	response := "+CCFC: 1,1,\"+8613800138000\",145\r\n+CCFC: 0,2\r\n+CCFC: 0,4"
	rule := parseCCFC(ForwardUnconditional, response)
	if !rule.Active {
		t.Fatal("voice forwarding was reported inactive")
	}
	if rule.Number != "+8613800138000" {
		t.Fatalf("number = %q", rule.Number)
	}
}

func TestAForwardedFaxClassDoesNotMakeCallsForwarded(t *testing.T) {
	// The inverse, and the one that matters: fax is forwarded, voice is not.
	// The fax line comes FIRST on purpose. With the voice line first this test
	// passes even without class filtering, which is how it was written the
	// first time and why it proved nothing -- verified by removing the class
	// check and watching it still pass.
	response := "+CCFC: 1,4,\"+8613800138000\",145\r\n+CCFC: 0,1"
	rule := parseCCFC(ForwardUnconditional, response)
	if rule.Active {
		t.Fatal("a forwarded fax class was read as forwarded calls")
	}
	if rule.Number != "" {
		t.Fatalf("a number was taken from the fax line: %q", rule.Number)
	}
}

func TestClassSevenCountsAsVoice(t *testing.T) {
	// Some modems answer a class-1 query with class 7, "voice and data and
	// fax". That does include voice, so refusing it would report forwarding as
	// off on those modems.
	rule := parseCCFC(ForwardBusy, "+CCFC: 1,7,\"13800138000\",129")
	if !rule.Active || rule.Number != "13800138000" {
		t.Fatalf("class 7 was not read as covering voice: %+v", rule)
	}
}

func TestTheNoReplyDelayIsReadFromItsOwnField(t *testing.T) {
	// The delay is the seventh field, after two optional subaddress fields the
	// modem leaves as empty commas. Counting fields rather than indexing them
	// would read the delay as a subaddress or miss it.
	rule := parseCCFC(ForwardNoReply, "+CCFC: 1,1,\"+8613800138000\",145,,,20")
	if rule.Seconds != 20 {
		t.Fatalf("seconds = %d; want 20", rule.Seconds)
	}
	// And it is only meaningful for no-reply: the same response shape against
	// another reason must not invent a delay.
	other := parseCCFC(ForwardUnconditional, "+CCFC: 1,1,\"+8613800138000\",145,,,20")
	if other.Seconds != 0 {
		t.Fatalf("unconditional forwarding reported a delay of %d", other.Seconds)
	}
}

func TestAnEmptyAnswerMeansNotSetRatherThanAnError(t *testing.T) {
	// A modem that answers a query with a bare OK is saying the condition is
	// not registered. Treating that as a failure would show an error on a
	// perfectly healthy line with no forwarding.
	rule := parseCCFC(ForwardUnreachable, "OK")
	if rule.Active || rule.Number != "" {
		t.Fatalf("a bare OK produced %+v", rule)
	}
}

func TestCallWaitingDistinguishesOffFromUnknown(t *testing.T) {
	// "Off" and "the modem did not say" are different facts. Collapsing them
	// means telling an operator a service is disabled when it was never
	// reported, which is exactly the kind of confident wrong answer that
	// starts a support thread.
	active, known := parseCCWA("+CCWA: 1,1\r\n+CCWA: 0,2")
	if !active || !known {
		t.Fatalf("enabled call waiting read as active=%v known=%v", active, known)
	}
	active, known = parseCCWA("+CCWA: 0,1")
	if active || !known {
		t.Fatalf("disabled call waiting read as active=%v known=%v", active, known)
	}
	active, known = parseCCWA("OK")
	if active || known {
		t.Fatalf("a silent modem read as active=%v known=%v; want unknown", active, known)
	}
	active, known = parseCCWA("+CCWA: 1,2\r\n+CCWA: 1,4")
	if active || known {
		t.Fatal("call waiting on data and fax was read as voice call waiting")
	}
}

func TestTurningForwardingOffErasesRatherThanDisables(t *testing.T) {
	// Mode 0 disables a rule but leaves the number registered on the network,
	// so a handset could re-enable it later and the operator who switched it
	// off here would never know. Mode 4 erases it.
	command, err := forwardingCommand(ForwardUnconditional, false, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(command, "AT+CCFC=0,4") {
		t.Fatalf("off used %q; mode 4 (erasure) is required", command)
	}
}

func TestTurningForwardingOnRegistersTheNumber(t *testing.T) {
	// Mode 1 enables a rule the network already holds. Setting a destination
	// for the first time with mode 1 reports success and forwards nothing,
	// because there is no registered number to enable. Mode 3 registers and
	// enables in one step.
	command, err := forwardingCommand(ForwardUnconditional, true, "+8613800138000", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(command, "AT+CCFC=0,3,") {
		t.Fatalf("on used %q; mode 3 (registration) is required", command)
	}
	// 145 is international form. Sending a leading + with type 129 gets the
	// registration rejected or misrouted by some networks.
	if !strings.Contains(command, ",145,") {
		t.Fatalf("an international number was not given type 145: %q", command)
	}
	national, err := forwardingCommand(ForwardBusy, true, "13800138000", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(national, ",129,") {
		t.Fatalf("a national number was not given type 129: %q", national)
	}
}

func TestTheNoReplyDelayIsOnlySentForNoReply(t *testing.T) {
	delayed, err := forwardingCommand(ForwardNoReply, true, "13800138000", 20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(delayed, ",,,20") {
		t.Fatalf("the delay was not appended: %q", delayed)
	}
	// A delay on a condition that has none is a parameter the modem will
	// reject, so it must not be sent.
	other, err := forwardingCommand(ForwardBusy, true, "13800138000", 20)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(other, ",,,20") {
		t.Fatalf("a delay was sent for a condition that has none: %q", other)
	}
}

// The number arrives from an HTTP request and ends up inside a quoted AT
// parameter. A quote closes the parameter and a carriage return ends the
// command, so either would let a caller append a second AT command of their
// choosing to the modem. This is the one test in this file guarding a security
// property rather than a correct reading.
func TestAForwardingNumberCannotSmuggleAnATCommand(t *testing.T) {
	for _, attempt := range []string{
		"13800138000\"",
		"1380013800\r\nAT+CFUN=0",
		"13800\x00138000",
		"\";AT+CPWROFF",
		"1380013800;AT+CCFC=0,4",
		"+861380013+8000",
		"13800138000+",
		strings.Repeat("1", 33),
	} {
		if _, err := forwardingCommand(ForwardUnconditional, true, attempt, 0); err == nil {
			t.Fatalf("%q was accepted as a forwarding destination", attempt)
		}
	}
	// And the legitimate shapes still work, or the guard is useless.
	for _, good := range []string{"+8613800138000", "13800138000", "*21#", "10086"} {
		if _, err := forwardingCommand(ForwardUnconditional, true, good, 0); err != nil {
			t.Fatalf("%q was refused: %v", good, err)
		}
	}
}

func TestEnablingForwardingRequiresADestination(t *testing.T) {
	// An empty number with mode 3 is a command the network answers with an
	// error the operator cannot interpret. Refusing here says what is wrong.
	if _, err := forwardingCommand(ForwardUnconditional, true, "   ", 0); err == nil {
		t.Fatal("forwarding was enabled with no destination")
	}
}

func TestTheNoReplyDelayIsRangeChecked(t *testing.T) {
	for _, bad := range []int{1, 4, 31, 120} {
		if err := validNoReplyDelay(bad); err == nil {
			t.Fatalf("%d seconds was accepted", bad)
		}
	}
	for _, good := range []int{0, 5, 20, 30} {
		if err := validNoReplyDelay(good); err != nil {
			t.Fatalf("%d seconds was refused: %v", good, err)
		}
	}
}

func TestAnUnknownReasonNameIsRefused(t *testing.T) {
	// A typo defaulting to "unconditional" would set up blanket forwarding and
	// divert every call on the line.
	if _, err := ParseForwardReason("uncondtional"); err == nil {
		t.Fatal("a misspelled reason was accepted")
	}
	if _, err := ParseForwardReason(""); err == nil {
		t.Fatal("an empty reason was accepted")
	}
	for name, want := range map[string]ForwardReason{
		"unconditional": ForwardUnconditional,
		"busy":          ForwardBusy,
		"no_reply":      ForwardNoReply,
		"unreachable":   ForwardUnreachable,
	} {
		got, err := ParseForwardReason(name)
		if err != nil || got != want {
			t.Fatalf("%q parsed as %v (%v)", name, got, err)
		}
		if got.String() != name {
			t.Fatalf("%v rendered as %q", got, got.String())
		}
	}
}

func TestQuotedFieldsSurviveSplitting(t *testing.T) {
	fields := splitATFields(`1,1,"+86,138,00138000",145`)
	if len(fields) != 4 {
		t.Fatalf("fields = %d: %q", len(fields), fields)
	}
	if fields[2] != `"+86,138,00138000"` {
		t.Fatalf("the quoted field was split: %q", fields[2])
	}
	// Empty fields have to survive, because the fields after them are found by
	// position.
	empty := splitATFields("1,1,,145,,,20")
	if len(empty) != 7 || empty[6] != "20" {
		t.Fatalf("empty fields collapsed: %q", empty)
	}
}
