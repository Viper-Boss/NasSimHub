package modemmanager

import (
	"fmt"
	"strconv"
	"strings"
)

// Parsing the network's answers about call forwarding and call waiting.
//
// These are GSM 07.07 supplementary services, and the two things worth knowing
// before reading any of this:
//
// They live on the NETWORK, not in the modem and not on this disk. Querying
// one is a round trip to the operator's HLR, so it is slow, it fails when the
// modem is not registered, and it cannot be cached as settings -- a rule set
// from a handset last year is still in force and this software has to read it
// rather than assume it knows.
//
// And the answers are per CLASS. A modem reports forwarding separately for
// voice, data and fax, and the same query can return three lines that disagree.
// Everything here keeps voice and drops the rest, because this is a voice
// gateway: showing a fax forwarding rule as if it governed calls would be
// worse than showing nothing.

// ForwardReason is which condition a forwarding rule applies to.
//
// The numbers are the <reason> parameter of AT+CCFC and are fixed by the
// standard, not chosen here.
type ForwardReason int

const (
	// ForwardUnconditional (CFU) forwards every call without ringing here.
	ForwardUnconditional ForwardReason = 0
	// ForwardBusy (CFB) forwards when this line is already on a call.
	ForwardBusy ForwardReason = 1
	// ForwardNoReply (CFNRy) forwards after ringing unanswered for <time>.
	ForwardNoReply ForwardReason = 2
	// ForwardUnreachable (CFNRc) forwards when the modem is off or has no
	// coverage. This is the one that matters most for a gateway on a home
	// broadband connection, which is exactly what drops out.
	ForwardUnreachable ForwardReason = 3
	// forwardAll and forwardAllConditional are write-only shortcuts: the
	// standard allows them for erasure but a query against them is not
	// meaningful, so they are not exported as readable reasons.
	forwardAll            ForwardReason = 4
	forwardAllConditional ForwardReason = 5
)

// readableForwardReasons are the reasons worth querying, in the order a person
// would read them: the blanket rule first, then the conditions.
var readableForwardReasons = []ForwardReason{
	ForwardUnconditional,
	ForwardBusy,
	ForwardNoReply,
	ForwardUnreachable,
}

// String gives the wire name used in the HTTP API.
func (reason ForwardReason) String() string {
	switch reason {
	case ForwardUnconditional:
		return "unconditional"
	case ForwardBusy:
		return "busy"
	case ForwardNoReply:
		return "no_reply"
	case ForwardUnreachable:
		return "unreachable"
	case forwardAll:
		return "all"
	case forwardAllConditional:
		return "all_conditional"
	default:
		return "unknown"
	}
}

// ParseForwardReason maps a wire name back to its number.
//
// Unknown names are refused rather than defaulting to unconditional: a typo
// that silently set up blanket forwarding would divert every call on the line.
func ParseForwardReason(name string) (ForwardReason, error) {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "unconditional":
		return ForwardUnconditional, nil
	case "busy":
		return ForwardBusy, nil
	case "no_reply":
		return ForwardNoReply, nil
	case "unreachable":
		return ForwardUnreachable, nil
	default:
		return 0, fmt.Errorf("unknown forwarding reason %q", name)
	}
}

// voiceClass is the <class> bit for voice calls in 07.07. Class 7 means
// "voice, data and fax together"; asking for 1 keeps the answers to the only
// class this product carries.
const voiceClass = 1

// ForwardingRule is one condition's state as the network reports it.
type ForwardingRule struct {
	Reason ForwardReason `json:"reason"`
	// Active is whether the network is forwarding for this condition.
	Active bool `json:"active"`
	// Number is where calls go. Empty when inactive, and also empty when the
	// network reports active without a number, which some do -- in that case
	// Active is still true, because it is.
	Number string `json:"number"`
	// Seconds is the no-reply delay. Only meaningful for ForwardNoReply, and
	// zero elsewhere.
	Seconds int `json:"seconds,omitempty"`
}

// parseCCFC reads the response to AT+CCFC=<reason>,2.
//
// A real modem answers like this, one line per class:
//
//	+CCFC: 1,1,"+8613800138000",145
//	+CCFC: 0,2
//	+CCFC: 0,4
//
// which reads as: voice (class 1) is forwarded to that number, data (2) and
// fax (4) are not. A no-reply query adds the delay:
//
//	+CCFC: 1,1,"+8613800138000",145,,,20
//
// Fields after the number are optional and modems omit them inconsistently,
// including by leaving empty commas in the middle, so this indexes what it
// needs and tolerates the rest instead of requiring a field count.
func parseCCFC(reason ForwardReason, response string) ForwardingRule {
	rule := ForwardingRule{Reason: reason}
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "\r"))
		if !strings.HasPrefix(line, "+CCFC:") {
			continue
		}
		fields := splitATFields(strings.TrimSpace(strings.TrimPrefix(line, "+CCFC:")))
		if len(fields) < 2 {
			continue
		}
		class, err := strconv.Atoi(fields[1])
		if err != nil || class != voiceClass {
			// Some modems answer a class-1 query with class 7 ("all"), which
			// does include voice. Anything else is a class this product does
			// not carry.
			if err != nil || class != 7 {
				continue
			}
		}
		status, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		rule.Active = status == 1
		if len(fields) >= 3 {
			rule.Number = strings.Trim(fields[2], `"`)
		}
		// The delay is the 7th field when present. Modems that omit the
		// subaddress fields still leave the commas, so the index holds.
		if reason == ForwardNoReply && len(fields) >= 7 {
			if seconds, err := strconv.Atoi(fields[6]); err == nil {
				rule.Seconds = seconds
			}
		}
		return rule
	}
	// No +CCFC line for voice at all. A modem that answers "OK" with nothing
	// else is telling us the condition is not set, which is not an error.
	return rule
}

// parseCCWA reads the response to AT+CCWA=1,2,1.
//
// Answers look like:
//
//	+CCWA: 1,1
//	+CCWA: 0,2
//
// The first number is the status, the second the class. As with forwarding,
// only voice is kept.
func parseCCWA(response string) (bool, bool) {
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "\r"))
		if !strings.HasPrefix(line, "+CCWA:") {
			continue
		}
		fields := splitATFields(strings.TrimSpace(strings.TrimPrefix(line, "+CCWA:")))
		if len(fields) < 2 {
			continue
		}
		class, err := strconv.Atoi(fields[1])
		if err != nil || (class != voiceClass && class != 7) {
			continue
		}
		status, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		return status == 1, true
	}
	// found=false means the modem said nothing about voice call waiting. That
	// is reported as unknown rather than "off", because telling an operator a
	// service is off when it was never asked about is how a support case
	// starts.
	return false, false
}

// splitATFields splits a comma-separated AT parameter list, respecting quotes.
//
// A forwarding number can legitimately contain no commas, but the response
// format quotes it anyway and a naive Split would break on any modem that
// decided to include one. Empty fields are preserved, because their position
// is what identifies the fields after them.
func splitATFields(line string) []string {
	fields := []string{}
	var current strings.Builder
	quoted := false
	for _, symbol := range line {
		switch {
		case symbol == '"':
			quoted = !quoted
			current.WriteRune(symbol)
		case symbol == ',' && !quoted:
			fields = append(fields, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(symbol)
		}
	}
	fields = append(fields, strings.TrimSpace(current.String()))
	return fields
}

// numberType is the <type> parameter: 145 for a number in international form,
// 129 otherwise. Getting this wrong is not cosmetic -- a network handed a
// leading + with type 129 may reject the registration or forward to the wrong
// place, so it follows the number rather than being hardcoded.
func numberType(number string) int {
	if strings.HasPrefix(strings.TrimSpace(number), "+") {
		return 145
	}
	return 129
}

// forwardingCommand builds the AT+CCFC that registers or erases a rule.
//
// Registration uses mode 3 rather than mode 1. Mode 1 enables a rule the
// network already holds; mode 3 registers the number AND enables it, which is
// what an operator setting a destination for the first time means. Using mode 1
// there would report success and forward nothing, because there is no
// registered number to enable.
func forwardingCommand(reason ForwardReason, enabled bool, number string, seconds int) (string, error) {
	if !enabled {
		// Erasure (mode 4) rather than disable (mode 0): a disabled rule keeps
		// the number registered on the network, so "off" in this UI would leave
		// a destination behind that a handset could re-enable later. Turning it
		// off here means off.
		return fmt.Sprintf("AT+CCFC=%d,4,,,%d", int(reason), voiceClass), nil
	}
	number = strings.TrimSpace(number)
	if number == "" {
		return "", fmt.Errorf("a forwarding destination is required")
	}
	if err := validForwardingNumber(number); err != nil {
		return "", err
	}
	command := fmt.Sprintf(
		"AT+CCFC=%d,3,\"%s\",%d,%d",
		int(reason), number, numberType(number), voiceClass,
	)
	if reason == ForwardNoReply && seconds > 0 {
		// <subaddr> and <satype> are skipped with empty commas; the standard
		// allows it and every modem tested accepts it.
		command = fmt.Sprintf("%s,,,%d", command, seconds)
	}
	return command, nil
}

// validForwardingNumber refuses anything that could break out of the quoted
// parameter or send the modem a second command.
//
// This is the one place in this file where a mistake is a security problem
// rather than a wrong display: the number arrives from an HTTP request and ends
// up inside an AT command string. A quote would close the parameter and a
// carriage return would end the command, so both are refused outright rather
// than escaped -- there is no legitimate phone number containing either.
func validForwardingNumber(number string) error {
	if len(number) > 32 {
		return fmt.Errorf("forwarding destination is too long")
	}
	for _, symbol := range number {
		switch {
		case symbol >= '0' && symbol <= '9':
		case symbol == '+' || symbol == '*' || symbol == '#':
		default:
			return fmt.Errorf("forwarding destination may only contain digits, +, * and #")
		}
	}
	if strings.Count(number, "+") > 1 || (strings.Contains(number, "+") && !strings.HasPrefix(number, "+")) {
		return fmt.Errorf("+ is only valid at the start of a number")
	}
	return nil
}

// noReplyDelaySeconds bounds the <time> parameter. The standard allows 1..30
// in steps of 5 and networks round; refusing out-of-range values here gives a
// clear error instead of a +CME ERROR the operator cannot interpret.
func validNoReplyDelay(seconds int) error {
	if seconds == 0 {
		return nil
	}
	if seconds < 5 || seconds > 30 {
		return fmt.Errorf("no-reply delay must be between 5 and 30 seconds")
	}
	return nil
}
