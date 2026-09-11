package modemmanager

import (
	"testing"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

// Phantom CLCC entries, and why they broke call audio.
//
// Measured on a QDC507 with nobody on a call, AT+CLCC answered with two
// entries in state 0 (active), voice mode, no number, type 128. They are not
// calls. Something in the module's call stack leaves them behind.
//
// On their own they are cosmetic. The damage came from projectKnownATCalls,
// which let an outgoing record with a BLANK NUMBER claim the call that had
// just been dialled:
//
//	record.direction == "outgoing" && (record.number == "" || numbersEqual(...))
//
// That clause exists for a real reason -- some modems report a freshly dialled
// call before they echo the number back -- but a phantom is also an outgoing
// record with a blank number. So on the first CLCC poll after ATD, the phantom
// at index 1 took the new call's ID and the pending entry was consumed. The
// server then believed the call was index 1, already active, and pointed the
// media activation, the hangup and the call lease at it. The REAL call arrived
// later at its own index as a separate call nobody was listening to.
//
// Which produces exactly the reported symptom: the call connects, instantly,
// and neither side can hear anything.
//
// The discriminator is the state. A call that has just been dialled cannot be
// ACTIVE yet -- it has to pass through dialing or alerting first. So a blank
// number may still claim the pending call, but only from a pre-connection
// state, which is the case the clause was written for and which a phantom can
// never be in.

// phantomCLCC is the response observed on the real module at idle.
const phantomCLCC = "+CLCC: 1,0,0,0,0,\"\",128\r\n+CLCC: 2,0,0,0,0,\"\",128\r\nOK"

func TestPhantomActiveCallsDoNotStealTheDialledCall(t *testing.T) {
	t.Parallel()

	provider := newTestProvider(newFakeCaller(emptyLineObjects(false, true)))
	line := domain.Line{ID: "line-1"}

	// The state after ATD was accepted: one call dialled and pending.
	provider.atCallStateMu.Lock()
	provider.atPendingCalls[line.ID] = atCallLifecycle{
		id:        "call-real",
		number:    "13800138000",
		direction: "outgoing",
	}
	provider.atCallStateMu.Unlock()

	records, err := parseQuectelCLCC(phantomCLCC)
	if err != nil {
		t.Fatalf("the phantom response did not parse: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected the two phantom records, got %d", len(records))
	}

	state := ParsedObjects{ATCallLines: make(map[string]string)}
	provider.projectKnownATCalls(&state, &line, records, true)

	// The pending call must NOT have been handed to a phantom.
	if _, found := findCall(state.Calls, "call-real"); found {
		t.Fatal(
			"a phantom active CLCC entry claimed the dialled call's ID; " +
				"media and hangup would target the phantom instead of the real call",
		)
	}
	provider.atCallStateMu.Lock()
	_, stillPending := provider.atPendingCalls[line.ID]
	provider.atCallStateMu.Unlock()
	if !stillPending {
		t.Fatal("the pending call was consumed by a phantom, so the real call can never be matched")
	}
}

// The clause the fix narrows is there for a real modem behaviour: a call
// reported before its number is echoed back. That still has to work, or every
// outgoing call on such a modem becomes unmatchable.
func TestADiallingRecordWithNoNumberYetStillClaimsThePendingCall(t *testing.T) {
	t.Parallel()

	for name, response := range map[string]string{
		// status 2 = dialing, status 3 = alerting. Both are pre-connection.
		"dialing":  "+CLCC: 1,0,2,0,0,\"\",128\r\nOK",
		"alerting": "+CLCC: 1,0,3,0,0,\"\",128\r\nOK",
	} {
		provider := newTestProvider(newFakeCaller(emptyLineObjects(false, true)))
		line := domain.Line{ID: "line-1"}
		provider.atCallStateMu.Lock()
		provider.atPendingCalls[line.ID] = atCallLifecycle{
			id:        "call-real",
			number:    "13800138000",
			direction: "outgoing",
		}
		provider.atCallStateMu.Unlock()

		records, err := parseQuectelCLCC(response)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		state := ParsedObjects{ATCallLines: make(map[string]string)}
		provider.projectKnownATCalls(&state, &line, records, true)

		if _, found := findCall(state.Calls, "call-real"); !found {
			t.Fatalf(
				"%s: a %s record with no number yet did not claim the pending call, "+
					"so an outgoing call on this modem could never be matched",
				name, name,
			)
		}
	}
}

// A record that echoes the dialled number claims the call whatever its state,
// because the number is a positive identification and no phantom has one.
func TestARecordMatchingTheDialledNumberStillClaimsTheCall(t *testing.T) {
	t.Parallel()

	provider := newTestProvider(newFakeCaller(emptyLineObjects(false, true)))
	line := domain.Line{ID: "line-1"}
	provider.atCallStateMu.Lock()
	provider.atPendingCalls[line.ID] = atCallLifecycle{
		id:        "call-real",
		number:    "13800138000",
		direction: "outgoing",
	}
	provider.atCallStateMu.Unlock()

	records, err := parseQuectelCLCC("+CLCC: 1,0,0,0,0,\"13800138000\",129\r\nOK")
	if err != nil {
		t.Fatal(err)
	}
	state := ParsedObjects{ATCallLines: make(map[string]string)}
	provider.projectKnownATCalls(&state, &line, records, true)
	if _, found := findCall(state.Calls, "call-real"); !found {
		t.Fatal("an active record with the dialled number did not claim the pending call")
	}
}

// And the real call must still be matchable once it appears alongside the
// phantoms, which is the whole point of not consuming the pending entry.
func TestTheRealCallIsStillMatchedWhenItAppearsBesidePhantoms(t *testing.T) {
	t.Parallel()

	provider := newTestProvider(newFakeCaller(emptyLineObjects(false, true)))
	line := domain.Line{ID: "line-1"}
	provider.atCallStateMu.Lock()
	provider.atPendingCalls[line.ID] = atCallLifecycle{
		id:        "call-real",
		number:    "13800138000",
		direction: "outgoing",
	}
	provider.atCallStateMu.Unlock()

	// First poll: only phantoms. The pending call survives.
	phantoms, err := parseQuectelCLCC(phantomCLCC)
	if err != nil {
		t.Fatal(err)
	}
	first := ParsedObjects{ATCallLines: make(map[string]string)}
	provider.projectKnownATCalls(&first, &line, phantoms, true)

	// Second poll: the real call has appeared at its own index, dialling.
	withReal, err := parseQuectelCLCC(
		"+CLCC: 1,0,0,0,0,\"\",128\r\n" +
			"+CLCC: 2,0,0,0,0,\"\",128\r\n" +
			"+CLCC: 3,0,2,0,0,\"13800138000\",129\r\nOK",
	)
	if err != nil {
		t.Fatal(err)
	}
	second := ParsedObjects{ATCallLines: make(map[string]string)}
	provider.projectKnownATCalls(&second, &line, withReal, true)

	call, found := findCall(second.Calls, "call-real")
	if !found {
		t.Fatal("the real call was never matched to the pending ID")
	}
	// And it must be the record with the number, not a phantom.
	if call.Number != "+8613800138000" && call.Number != "13800138000" {
		t.Fatalf("the matched call has number %q; it matched a phantom", call.Number)
	}
}
