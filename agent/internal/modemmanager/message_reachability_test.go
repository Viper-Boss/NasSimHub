package modemmanager

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSMSStorageUsageSkipsStatusReportStorage(t *testing.T) {
	t.Parallel()

	for response, want := range map[string]int{
		"+CPMS: \"ME\",0,23,\"ME\",0,23,\"ME\",0,23": 0,
		// The first triple is the status report storage: a delivery report is
		// not a reception, and ModemManager tracks it separately.
		"+CPMS: \"SR\",4,5,\"ME\",1,23,\"ME\",1,23":  1,
		"+CPMS: \"ME\",2,23,\"ME\",2,23,\"ME\",2,23": 2,
		"+CPMS: \"SM\",3,20,\"ME\",1,23,\"ME\",1,23": 3,
	} {
		used, known := smsStorageUsage(response)
		if !known || used != want {
			t.Fatalf("smsStorageUsage(%q) = %d, %t, want %d, true", response, used, known, want)
		}
	}

	for _, response := range []string{"", "OK", "+CPMS: \"ME\""} {
		if _, known := smsStorageUsage(response); known {
			t.Fatalf("smsStorageUsage(%q) reported a count", response)
		}
	}
}

func TestSMSServiceValueReadsDomain(t *testing.T) {
	t.Parallel()

	for response, want := range map[string]int{
		"+CGSMS: 1":  1,
		"+CGSMS:  2": 2,
		"+CGSMS: 3":  3,
	} {
		service, known := smsServiceValue(response)
		if !known || service != want {
			t.Fatalf("smsServiceValue(%q) = %d, %t, want %d, true", response, service, known, want)
		}
	}
	if _, known := smsServiceValue("OK"); known {
		t.Fatal("smsServiceValue reported a domain for an empty answer")
	}
}

func TestSMSReachabilityStateOnlyFlagsPersistentDivergence(t *testing.T) {
	t.Parallel()

	base := time.Unix(1_700_000_000, 0)
	state := &smsReachabilityState{}
	if state.diverged(0, 0, base) {
		t.Fatal("an empty storage was treated as a lost message")
	}
	// The storage gains a message ModemManager never reported: the first pass
	// only records the suspicion.
	if state.diverged(1, 0, base.Add(time.Minute)) {
		t.Fatal("the first pass already cycled the radio")
	}
	if !state.diverged(1, 0, base.Add(time.Minute+smsStorageSettleDelay)) {
		t.Fatal("a divergence that survived the settle delay did not trigger recovery")
	}

	// A divergence that clears on its own (the healthy pipeline ingests and
	// deletes the message) must not leave a stale suspicion behind.
	cleared := &smsReachabilityState{}
	cleared.diverged(1, 0, base)
	if cleared.diverged(1, 1, base.Add(time.Minute)) {
		t.Fatal("a reported message still triggered recovery")
	}
	if cleared.diverged(1, 0, base.Add(2*time.Minute)) {
		t.Fatal("a cleared divergence was reused as an old suspicion")
	}
}

func TestSMSReachabilityStateIgnoresOwnSentCopiesAndRepeats(t *testing.T) {
	t.Parallel()

	base := time.Unix(1_700_000_000, 0)
	state := &smsReachabilityState{}
	// Sending stores the modem's own copy while ModemManager drops its object;
	// that is not a reception the user is waiting for.
	state.noteSent(base)
	if state.diverged(1, 0, base.Add(10*time.Second)) {
		t.Fatal("the agent's own sent copy triggered recovery")
	}

	suspect := base.Add(smsSendQuietWindow + time.Second)
	if state.diverged(1, 0, suspect) {
		t.Fatal("the first pass after the quiet window already cycled the radio")
	}
	recovered := suspect.Add(smsStorageSettleDelay)
	if !state.diverged(1, 0, recovered) {
		t.Fatal("a divergence after the quiet window did not trigger recovery")
	}

	// One cycle per rate limit window, even when the storage keeps growing.
	state.markCycled(recovered)
	next := recovered.Add(30 * time.Second)
	if state.diverged(2, 0, next) {
		t.Fatal("the first pass after a cycle already cycled again")
	}
	if state.diverged(2, 0, next.Add(smsStorageSettleDelay)) {
		t.Fatal("the rate limit did not hold a second cycle back")
	}
}

func TestEnsureURCPortBlindWritesWhenUnreadable(t *testing.T) {
	t.Parallel()

	caller := newFakeCaller(emptyLineObjects(true, true))
	caller.atCommandErrors[urcPortQuery] = errors.New("Unknown error")
	provider := newTestProvider(caller)
	parsed, err := provider.snapshotContent(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	line, _, found := messageLine(parsed)
	if !found {
		t.Fatal("no message line in the fixture")
	}

	if err := provider.ensureURCPort(context.Background(), line.ID); err != nil {
		t.Fatalf("ensureURCPort() error = %v", err)
	}
	writes := 0
	for _, invocation := range caller.invocations() {
		if invocation.Method != modemInterface+".Command" || len(invocation.Args) == 0 {
			continue
		}
		if command, ok := invocation.Args[0].(string); ok && command == urcPortWrite {
			writes++
		}
	}
	if writes != 1 {
		t.Fatalf("blind writes = %d, want 1", writes)
	}

	// The throttle holds a second immediate pass back: the read fails again and
	// the error propagates (the caller only logs it at debug level), but no
	// second write happens.
	_ = provider.ensureURCPort(context.Background(), line.ID)
	writes = 0
	for _, invocation := range caller.invocations() {
		if invocation.Method != modemInterface+".Command" || len(invocation.Args) == 0 {
			continue
		}
		if command, ok := invocation.Args[0].(string); ok && command == urcPortWrite {
			writes++
		}
	}
	if writes != 1 {
		t.Fatalf("blind writes after the throttle = %d, want 1", writes)
	}
}

func TestReconcileSMSReachabilityCyclesRadioForUnreportedStorage(t *testing.T) {
	t.Parallel()

	caller := newFakeCaller(emptyLineObjects(true, true))
	caller.atResponses = map[string]string{
		smsStorageQuery: "+CPMS: \"ME\",1,23,\"ME\",1,23,\"ME\",1,23",
		smsServiceQuery: "+CGSMS: 2",
	}
	provider := newTestProvider(caller)
	// A previous pass already saw the divergence and it is still unreported, so
	// this pass has to recover the message.
	provider.smsState.suspectedAt = time.Now().Add(-time.Minute)

	if err := provider.ReconcileSMSReachability(context.Background()); err != nil {
		t.Fatalf("ReconcileSMSReachability() error = %v", err)
	}
	invocations := caller.invocations()
	if got := countMethod(invocations, modemInterface+".Enable"); got != 2 {
		t.Fatalf("radio Enable calls = %d, want 2 (disable and enable): %#v", got, invocations)
	}
	// The indication port must be rewritten right after the cycle: re-enabling
	// the modem is when the firmware resets its indication routing.
	rewriteIndex, enableIndex := -1, -1
	for index, invocation := range invocations {
		if invocation.Method != modemInterface+".Command" || len(invocation.Args) == 0 {
			continue
		}
		if command, ok := invocation.Args[0].(string); !ok {
			continue
		} else if command == urcPortWrite && rewriteIndex == -1 {
			rewriteIndex = index
		}
		if invocation.Method == modemInterface+".Enable" {
			enableIndex = index
		}
	}
	if rewriteIndex == -1 {
		t.Fatalf("the indication port was not rewritten after the cycle: %#v", invocations)
	}
	if rewriteIndex < enableIndex {
		t.Fatal("the indication port was rewritten before the radio was re-enabled")
	}
}

func TestReconcileSMSReachabilityLeavesMatchingStorageAlone(t *testing.T) {
	t.Parallel()

	caller := newFakeCaller(emptyLineObjects(true, true))
	caller.atResponses = map[string]string{
		smsStorageQuery: "+CPMS: \"ME\",0,23,\"ME\",0,23,\"ME\",0,23",
		smsServiceQuery: "+CGSMS: 2",
	}
	provider := newTestProvider(caller)
	provider.smsState.suspectedAt = time.Now().Add(-time.Minute)

	if err := provider.ReconcileSMSReachability(context.Background()); err != nil {
		t.Fatalf("ReconcileSMSReachability() error = %v", err)
	}
	if got := countMethod(caller.invocations(), modemInterface+".Enable"); got != 0 {
		t.Fatalf("radio Enable calls = %d, want 0", got)
	}
}

func TestReconcileSMSReachabilityCorrectsCircuitSwitchedOnlyService(t *testing.T) {
	t.Parallel()

	caller := newFakeCaller(emptyLineObjects(true, true))
	caller.atResponses = map[string]string{
		smsStorageQuery: "+CPMS: \"ME\",0,23,\"ME\",0,23,\"ME\",0,23",
		smsServiceQuery: "+CGSMS: 1",
	}
	provider := newTestProvider(caller)

	if err := provider.ReconcileSMSReachability(context.Background()); err != nil {
		t.Fatalf("ReconcileSMSReachability() error = %v", err)
	}
	corrected := false
	for _, invocation := range caller.invocations() {
		if invocation.Method != modemInterface+".Command" || len(invocation.Args) == 0 {
			continue
		}
		if command, ok := invocation.Args[0].(string); ok && command == smsServiceWrite {
			corrected = true
		}
	}
	if !corrected {
		t.Fatalf("circuit switched only SMS domain was not corrected: %#v", caller.invocations())
	}
	if got := countMethod(caller.invocations(), modemInterface+".Enable"); got != 0 {
		t.Fatalf("radio Enable calls = %d, want 0 for an empty storage", got)
	}
}

func TestURCPortValue(t *testing.T) {
	t.Parallel()

	port, known := urcPortValue(`+QURCCFG: "urcport","usbat"`)
	if !known || port != "usbat" {
		t.Fatalf("usbat: known = %v port = %q", known, port)
	}
	port, known = urcPortValue(`+QURCCFG: "urcport","all"`)
	if !known || port != urcPortExpected {
		t.Fatalf("all: known = %v port = %q", known, port)
	}
	port, known = urcPortValue(`+QURCCFG: "urcport","uart1"`)
	if !known || port != "uart1" {
		t.Fatalf("uart1: known = %v port = %q", known, port)
	}
	for _, response := range []string{"", "OK", "+QURCCFG: \"urcport\""} {
		if _, known := urcPortValue(response); known {
			t.Fatalf("urcPortValue(%q) reported a known port", response)
		}
	}
}

func TestReconcileSMSReachabilityCorrectsIndicationPort(t *testing.T) {
	t.Parallel()

	caller := newFakeCaller(emptyLineObjects(true, true))
	caller.atResponses = map[string]string{
		smsStorageQuery: "+CPMS: \"ME\",0,23,\"ME\",0,23,\"ME\",0,23",
		smsServiceQuery: "+CGSMS: 2",
		urcPortQuery:    "+QURCCFG: \"urcport\",\"usbat\"",
	}
	provider := newTestProvider(caller)

	if err := provider.ReconcileSMSReachability(context.Background()); err != nil {
		t.Fatalf("ReconcileSMSReachability() error = %v", err)
	}
	corrected := false
	for _, invocation := range caller.invocations() {
		if invocation.Method != modemInterface+".Command" || len(invocation.Args) == 0 {
			continue
		}
		if command, ok := invocation.Args[0].(string); ok && command == urcPortWrite {
			corrected = true
		}
	}
	if !corrected {
		t.Fatalf("the indication port was not corrected: %#v", caller.invocations())
	}
}

func TestReconcileSMSReachabilityLeavesEveryPortIndicationAlone(t *testing.T) {
	t.Parallel()

	caller := newFakeCaller(emptyLineObjects(true, true))
	caller.atResponses = map[string]string{
		smsStorageQuery: "+CPMS: \"ME\",0,23,\"ME\",0,23,\"ME\",0,23",
		smsServiceQuery: "+CGSMS: 2",
		urcPortQuery:    "+QURCCFG: \"urcport\",\"all\"",
	}
	provider := newTestProvider(caller)

	if err := provider.ReconcileSMSReachability(context.Background()); err != nil {
		t.Fatalf("ReconcileSMSReachability() error = %v", err)
	}
	for _, invocation := range caller.invocations() {
		if invocation.Method != modemInterface+".Command" || len(invocation.Args) == 0 {
			continue
		}
		if command, ok := invocation.Args[0].(string); ok && command == urcPortWrite {
			t.Fatal("the indication port was written although it already reports every port")
		}
	}
}

// The unreadable-port case is covered by TestEnsureURCPortBlindWritesWhenUnreadable:
// since r79b an unreadable query leads to a throttled blind rewrite instead of
// an inactive guard, because the firmware stops answering the query once the
// port is on "all".
