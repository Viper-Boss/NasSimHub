// Package trustengine is the gateway's record of how far each NasSimHub Node
// may be used, and of every event that changed it.
//
// The product serves one household's own SIMs. A device that was just adopted,
// or whose SIM was just swapped, is not yet known to be that; so it starts in
// an observation period in which it receives calls and messages but the
// gateway does not place calls or send messages through it. Stable use earns
// TRUSTED. Things that look like a SIM farm - SIMs changing, the device
// identity changing, a flood of verification codes - move it back down, and
// only an administrator moves it back up, into observation, never straight to
// TRUSTED.
//
// Three rules shape the code:
//
//   - Going down is automatic; going up is not. Nothing in this package
//     promotes a restricted or quarantined subject without an administrator's
//     request, and that request is itself recorded.
//   - Every state change is an event in a hash chain, and every record is
//     signed. A record that does not verify, or whose last event is not in the
//     chain, is treated as RESTRICTED: damage fails closed, and says so.
//   - It decides only from evidence it has. There is no location rule, no
//     hidden permanent ban and nothing destructive: the strongest action is to
//     stop placing outgoing calls and messages through one device.
//
// The rules live here, not in the call or SMS code. Callers ask Require.
package trustengine

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// State is a subject's trust state.
type State string

const (
	Unbound     State = "UNBOUND"
	Observation State = "OBSERVATION"
	Trusted     State = "TRUSTED"
	Restricted  State = "RESTRICTED"
	Quarantine  State = "QUARANTINE"
)

// rank orders states from least to most restrictive for outgoing use.
func rank(state State) int {
	switch state {
	case Trusted:
		return 0
	case Observation:
		return 1
	case Restricted:
		return 2
	case Quarantine:
		return 3
	}
	return 4 // UNBOUND and anything unrecognised: nothing outgoing
}

// MostRestrictive returns the more restrictive of two states.
func MostRestrictive(a, b State) State {
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// Action is something a caller wants to do through a subject.
type Action string

const (
	ActionReceiveCall Action = "receive_call"
	ActionReceiveSMS  Action = "receive_sms"
	ActionDial        Action = "dial"
	ActionSendSMS     Action = "send_sms"
	ActionForwardOTP  Action = "forward_otp"
	// ActionDTMF is sending tones into a call. It is outgoing use, decided
	// like dialling. It is not in Actions: no view lists it yet.
	ActionDTMF     Action = "dtmf"
	ActionUpdate   Action = "update"
	ActionDiagnose Action = "diagnose"
)

// Actions lists every action, for views.
var Actions = []Action{ActionReceiveCall, ActionReceiveSMS, ActionDial, ActionSendSMS, ActionForwardOTP, ActionUpdate, ActionDiagnose}

// Event kinds. They name what happened; none carries message text, a code, a
// full phone number or a SIM identifier.
const (
	EventBound            = "PAIRING_BOUND"
	EventGrandfathered    = "TRUST_GRANDFATHERED"
	EventSIMChanged       = "SIM_CHANGED"
	EventModemChanged     = "MODEM_CHANGED"
	EventIdentityChanged  = "PAIRING_CHANGED"
	EventIntegrityFailure = "INTEGRITY_FAILURE"
	EventOTPLimit         = "OTP_LIMIT_TRIGGERED"
	EventOTPCleared       = "OTP_LIMIT_CLEARED"
	EventDowngrade        = "TRUST_DOWNGRADE"
	EventUpgrade          = "TRUST_UPGRADE"
	EventManualRestrict   = "MANUAL_RESTRICT"
	EventManualQuarantine = "MANUAL_QUARANTINE"
	EventManualRestore    = "MANUAL_RESTORE"
	EventActionRefused    = "ACTION_REFUSED"
	EventChainResealed    = "AUDIT_CHAIN_RESEALED"
	EventRecordDamaged    = "TRUST_RECORD_DAMAGED"
)

// Policy holds the numbers. They are the master plan's starting values and are
// data so that tests, and later real-world tuning, do not edit logic.
type Policy struct {
	FirstObservation    time.Duration
	ExtendedObservation time.Duration
	LongestObservation  time.Duration
	// MaxAccrualStep bounds how much stable time one observation may credit,
	// so a gateway that was suspended cannot jump the whole period on resume.
	MaxAccrualStep time.Duration
	// Risk added per event.
	RiskSIMChange, RiskIdentityChange, RiskIntegrityFailure, RiskOTP uint32
	// RiskModemChange is added when a device with the same identity key
	// reports a different hardware identifier: its radio module was replaced.
	RiskModemChange uint32
	// Thresholds on the risk score.
	ExtendedAt, LongestAt, QuarantineAt uint32
	// RiskDecayEvery is how much trusted, healthy time removes one point.
	RiskDecayEvery time.Duration
	// OTP thresholds over 24 hours: more than Notice is recorded and scored,
	// more than Limit stops forwarding codes, more than High restricts.
	OTPNotice, OTPLimit, OTPHigh int
	// RefusalEventEvery bounds how often refused actions are written to the
	// chain per subject and action, so a retry loop cannot fill the journal.
	RefusalEventEvery time.Duration
}

// DefaultPolicy is the policy the gateway runs with.
func DefaultPolicy() Policy {
	return Policy{
		FirstObservation: 7 * 24 * time.Hour, ExtendedObservation: 14 * 24 * time.Hour, LongestObservation: 30 * 24 * time.Hour,
		MaxAccrualStep: 10 * time.Minute,
		RiskSIMChange:  2, RiskIdentityChange: 4, RiskIntegrityFailure: 5, RiskOTP: 3,
		RiskModemChange: 3,
		ExtendedAt:      5, LongestAt: 8, QuarantineAt: 20,
		RiskDecayEvery: 30 * 24 * time.Hour,
		OTPNotice:      5, OTPLimit: 10, OTPHigh: 20,
		RefusalEventEvery: time.Hour,
	}
}

func (p Policy) window(score uint32) time.Duration {
	switch {
	case score >= p.LongestAt:
		return p.LongestObservation
	case score >= p.ExtendedAt:
		return p.ExtendedObservation
	}
	return p.FirstObservation
}

// Record is one subject's persistent trust record. It holds hashes and
// counters only: BindingHash and SIMHash are keyed hashes made by the caller,
// never an ICCID, IMSI or number.
type Record struct {
	SubjectID         string    `json:"node_id"`
	State             State     `json:"state"`
	BindingHash       string    `json:"-"`
	SIMHash           string    `json:"-"`
	StableSeconds     uint64    `json:"stable_seconds"`
	RequiredSeconds   uint64    `json:"required_seconds"`
	TrustedSeconds    uint64    `json:"-"`
	SIMChanges        uint32    `json:"sim_changes"`
	IdentityChanges   uint32    `json:"identity_changes"`
	IntegrityFailures uint32    `json:"integrity_failures"`
	RiskScore         uint32    `json:"risk_score"`
	OTPLimited        bool      `json:"otp_limited"`
	OTPLevel          int       `json:"-"`
	Manual            bool      `json:"manual"`
	Reason            string    `json:"reason,omitempty"`
	Generation        uint64    `json:"generation"`
	LastEventHash     string    `json:"-"`
	UpdatedAt         time.Time `json:"updated_at"`
	Signature         []byte    `json:"-"`
	// PolicyGeneration is the generation of the last trust policy signed for
	// the device to enforce (proto.TrustPolicy). It only ever increases, and it
	// is stored here so that it survives a restart: a device refuses a policy
	// older than the one it holds.
	PolicyGeneration uint64 `json:"policy_generation"`
	// HardwareHash is a keyed hash of the hardware identifier the device
	// reported (itself a digest; the serial never reaches the gateway), or
	// empty when it has never reported one.
	HardwareHash string `json:"-"`
	ModemChanges uint32 `json:"modem_changes"`
}

// signedBytes is what a record's signature covers: every field.
//
// The fields added in r128 (policy generation, hardware hash, modem changes)
// are appended under a v2 label. A record in which all three are still zero is
// signed exactly as it was before they existed: that is every record written
// by an earlier gateway, whose signatures must keep verifying after the
// columns are added - otherwise an upgrade would mark every device's record
// damaged and restrict it. The two encodings cannot be confused, because the
// label is part of the signed bytes and v2 is used whenever any new field is
// set.
func (r Record) signedBytes() []byte {
	fields := []string{"nsh-trust-record-v1", r.SubjectID, string(r.State), r.BindingHash, r.SIMHash,
		fmt.Sprint(r.StableSeconds), fmt.Sprint(r.RequiredSeconds), fmt.Sprint(r.TrustedSeconds), fmt.Sprint(r.SIMChanges),
		fmt.Sprint(r.IdentityChanges), fmt.Sprint(r.IntegrityFailures), fmt.Sprint(r.RiskScore), fmt.Sprint(r.OTPLimited),
		fmt.Sprint(r.OTPLevel), fmt.Sprint(r.Manual), r.Reason, fmt.Sprint(r.Generation), r.LastEventHash,
		r.UpdatedAt.UTC().Format(time.RFC3339Nano)}
	if r.PolicyGeneration != 0 || r.HardwareHash != "" || r.ModemChanges != 0 {
		fields[0] = "nsh-trust-record-v2"
		fields = append(fields, fmt.Sprint(r.PolicyGeneration), r.HardwareHash, fmt.Sprint(r.ModemChanges))
	}
	return []byte(strings.Join(fields, "\x1f"))
}

// Event is one entry of the security journal.
type Event struct {
	Seq       int64     `json:"seq"`
	At        time.Time `json:"at"`
	SubjectID string    `json:"node_id,omitempty"`
	Kind      string    `json:"kind"`
	// Cause says why it happened; Source who or what decided; Result what was
	// done about it. Together they answer "why is this device restricted".
	Cause    string `json:"cause"`
	Source   string `json:"source"`
	Result   string `json:"result"`
	PrevHash string `json:"prev_hash"`
	Hash     string `json:"hash"`
}

// ChainHash computes an event's hash from its predecessor's.
func ChainHash(previous string, event Event) string {
	digest := sha256.New()
	digest.Write([]byte(previous))
	digest.Write([]byte{0})
	digest.Write([]byte(strings.Join([]string{fmt.Sprint(event.Seq), event.At.UTC().Format(time.RFC3339Nano),
		event.SubjectID, event.Kind, event.Cause, event.Source, event.Result}, "\x1f")))
	return hex.EncodeToString(digest.Sum(nil))
}

// Head is the signed tip of the journal. It is what makes a removed tail
// detectable: the chain alone cannot show that its last entries are missing.
type Head struct {
	Seq       int64
	Hash      string
	Signature []byte
	// SealedFrom is the first sequence number verification covers. It is zero
	// until an administrator reseals a damaged journal; see Reseal.
	SealedFrom int64
}

func (h Head) signedBytes() []byte {
	return []byte(fmt.Sprintf("nsh-security-head-v1\x1f%d\x1f%s\x1f%d", h.Seq, h.Hash, h.SealedFrom))
}

// Store persists records and the journal. Commit is one transaction: it
// assigns sequence numbers and hashes to the events (through seal, which the
// engine supplies so the store holds no policy), writes the record if there is
// one, and replaces the head.
type Store interface {
	Records(ctx context.Context) ([]Record, error)
	Head(ctx context.Context) (Head, bool, error)
	Events(ctx context.Context, afterSeq int64, limit int, subject string) ([]Event, error)
	EventsBefore(ctx context.Context, beforeSeq int64, limit int, subject string) ([]Event, error)
	EventsFrom(ctx context.Context, fromSeq int64, visit func(Event) error) error
	Commit(ctx context.Context, record *Record, events []Event,
		seal func(previous Head, events []Event, record *Record) (Head, error)) error
	Meta(ctx context.Context, key string) (string, bool, error)
	SetMeta(ctx context.Context, key, value string) error
}

// Enforcement says whether Require refuses or only records.
type Enforcement string

const (
	// Enforce refuses what the state does not allow.
	Enforce Enforcement = "enforce"
	// Monitor evaluates and records what WOULD be refused, and refuses
	// nothing. It exists for bringing up hardware; views say plainly that
	// restrictions are not being enforced.
	Monitor Enforcement = "monitor"
)

// Options configures an Engine.
type Options struct {
	Store Store
	// Key signs records and the journal head; it is the Core's identity key.
	Key         ed25519.PrivateKey
	Policy      Policy
	Enforcement Enforcement
	Now         func() time.Time
	// FlushEvery is how much accrued stable time is held in memory before the
	// record is rewritten. A restart loses at most this much credit.
	FlushEvery time.Duration
}

// Engine evaluates and records trust.
type Engine struct {
	store       Store
	key         ed25519.PrivateKey
	public      ed25519.PublicKey
	policy      Policy
	enforcement Enforcement
	now         func() time.Time
	flushEvery  time.Duration

	mu       sync.Mutex
	records  map[string]*Record
	damaged  map[string]string // subject -> why its record is not believed
	pending  map[string]uint64 // stable seconds accrued, not yet written
	refused  map[string]time.Time
	chain    ChainReport
	chainSet bool
	// onChange is told when what a device should enforce may have changed.
	onChange func(subject string)
}

// ErrRefused is returned by Require when an action is not allowed.
type ErrRefused struct {
	SubjectID string
	Action    Action
	State     State
	Reason    string
}

func (e *ErrRefused) Error() string {
	return fmt.Sprintf("%s is not allowed while this device is %s: %s", e.Action, e.State, e.Reason)
}

// New loads every record and verifies it.
func New(ctx context.Context, options Options) (*Engine, error) {
	if options.Store == nil || len(options.Key) != ed25519.PrivateKeySize {
		return nil, errors.New("trust engine requires a store and the core signing key")
	}
	engine := &Engine{
		store: options.Store, key: options.Key, public: options.Key.Public().(ed25519.PublicKey),
		policy: options.Policy, enforcement: options.Enforcement, now: options.Now, flushEvery: options.FlushEvery,
		records: map[string]*Record{}, damaged: map[string]string{}, pending: map[string]uint64{}, refused: map[string]time.Time{},
	}
	if engine.policy == (Policy{}) {
		engine.policy = DefaultPolicy()
	}
	if engine.enforcement == "" {
		engine.enforcement = Enforce
	}
	if engine.enforcement != Enforce && engine.enforcement != Monitor {
		return nil, fmt.Errorf("unknown trust enforcement %q; use enforce or monitor", engine.enforcement)
	}
	if engine.now == nil {
		engine.now = time.Now
	}
	if engine.flushEvery <= 0 {
		engine.flushEvery = 5 * time.Minute
	}
	records, err := engine.store.Records(ctx)
	if err != nil {
		return nil, err
	}
	for index := range records {
		record := records[index]
		engine.records[record.SubjectID] = &record
		if !ed25519.Verify(engine.public, record.signedBytes(), record.Signature) {
			engine.damaged[record.SubjectID] = "the stored trust record does not carry this gateway's signature"
		}
	}
	if _, err := engine.VerifyChain(ctx); err != nil {
		return nil, err
	}
	return engine, nil
}

// Enforcement reports the mode.
func (e *Engine) Enforcement() Enforcement { return e.enforcement }

func (e *Engine) seal(previous Head, events []Event, record *Record) (Head, error) {
	head := previous
	for index := range events {
		events[index].Seq = head.Seq + 1
		events[index].PrevHash = head.Hash
		events[index].Hash = ChainHash(head.Hash, events[index])
		head.Seq, head.Hash = events[index].Seq, events[index].Hash
	}
	if record != nil {
		if len(events) > 0 {
			record.LastEventHash = head.Hash
		}
		record.Signature = ed25519.Sign(e.key, record.signedBytes())
	}
	head.Signature = ed25519.Sign(e.key, head.signedBytes())
	return head, nil
}

func (e *Engine) event(subject, kind, cause, source, result string) Event {
	return Event{At: e.now().UTC(), SubjectID: subject, Kind: kind, Cause: Sanitize(cause), Source: Sanitize(source), Result: Sanitize(result)}
}

// Sanitize keeps identifying detail out of the journal: any run of seven or
// more digits (a phone number, an ICCID, an IMSI, a code with its context) is
// replaced, and the text is bounded. The journal explains decisions; it is not
// a place where subscriber data may accumulate.
func Sanitize(text string) string {
	var out strings.Builder
	run := 0
	flush := func(digits string) {
		if len(digits) >= 7 {
			out.WriteString("[redacted]")
		} else {
			out.WriteString(digits)
		}
	}
	start := 0
	for index, character := range text {
		if character >= '0' && character <= '9' {
			if run == 0 {
				start = index
			}
			run++
			continue
		}
		if run > 0 {
			flush(text[start:index])
			run = 0
		}
		if character == '\n' || character == '\r' || character == '\x1f' {
			character = ' '
		}
		out.WriteRune(character)
	}
	if run > 0 {
		flush(text[start:])
	}
	result := out.String()
	if len(result) > 240 {
		result = result[:240]
	}
	return result
}

// commit writes a changed record and its events, and updates memory only when
// the store accepted them.
func (e *Engine) commitLocked(ctx context.Context, record *Record, events []Event) error {
	if record != nil {
		record.Generation++
		record.UpdatedAt = e.now().UTC()
		record.StableSeconds += e.pending[record.SubjectID]
	}
	if err := e.store.Commit(ctx, record, events, e.seal); err != nil {
		if record != nil {
			record.Generation--
			record.StableSeconds -= e.pending[record.SubjectID]
		}
		return err
	}
	if record != nil {
		delete(e.pending, record.SubjectID)
		previous, known := e.records[record.SubjectID]
		_, wasDamaged := e.damaged[record.SubjectID]
		stored := *record
		e.records[record.SubjectID] = &stored
		delete(e.damaged, record.SubjectID)
		if e.onChange != nil && (!known || wasDamaged || previous.State != stored.State ||
			previous.OTPLimited != stored.OTPLimited || previous.Reason != stored.Reason) {
			// On its own goroutine: the listener asks the engine for the new
			// policy, and this runs with the engine locked.
			go e.onChange(stored.SubjectID)
		}
	}
	return nil
}

// Reading is what a poll of one subject established.
type Reading struct {
	SubjectID string
	// BindingHash covers Core, device identity key and (when present) SIM. It
	// changes when the device identity changes.
	BindingHash string
	// IdentityHash covers Core and device identity key only.
	IdentityHash string
	// SIMHash is a keyed hash of the SIM identity, or empty when no SIM is
	// readable. An empty hash is "not known now", not a change.
	SIMHash string
	// Healthy is whether this poll counts as stable use: reachable, identity
	// verified, compatible.
	Healthy bool
	// Elapsed is monotonic time since the previous observation of this
	// subject.
	Elapsed time.Duration
	// HardwareHash is a keyed hash of the device's reported hardware
	// identifier, or empty when the device reported none this time. Like the
	// SIM hash, empty is "not known now" and never a change.
	HardwareHash string
}

// Observe folds one poll into the subject's record.
func (e *Engine) Observe(ctx context.Context, observation Reading) error {
	subject := strings.TrimSpace(observation.SubjectID)
	if subject == "" || observation.IdentityHash == "" {
		return errors.New("an observation needs a subject and its identity hash")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	record, known := e.records[subject]
	if !known {
		created := Record{SubjectID: subject, State: Observation, BindingHash: observation.IdentityHash, SIMHash: observation.SIMHash,
			HardwareHash:    observation.HardwareHash,
			RequiredSeconds: uint64(e.policy.FirstObservation / time.Second), Reason: "first binding: observation period"}
		return e.commitLocked(ctx, &created, []Event{e.event(subject, EventBound, "the device was adopted by this gateway",
			"gateway", fmt.Sprintf("observation for %s; receiving only", e.policy.FirstObservation))})
	}
	if _, bad := e.damaged[subject]; bad {
		return nil // nothing is credited or changed on a record that is not believed
	}
	next := *record
	var events []Event

	if next.BindingHash != observation.IdentityHash {
		// The same device ID with another identity key. Pairing refuses that
		// without an administrator, so this follows a deliberate re-adoption.
		next.IdentityChanges++
		next.RiskScore += e.policy.RiskIdentityChange
		next.BindingHash, next.SIMHash = observation.IdentityHash, observation.SIMHash
		// Another identity key is, as far as this record can tell, another
		// device: its hardware is recorded afresh, not compared.
		next.HardwareHash = observation.HardwareHash
		events = append(events, e.event(subject, EventIdentityChanged, "the device identity key is not the one recorded", "gateway", "risk raised"))
		e.downgradeLocked(&next, &events, "device identity changed")
	} else {
		if observation.SIMHash != "" && next.SIMHash != observation.SIMHash {
			if next.SIMHash == "" {
				next.SIMHash = observation.SIMHash // first SIM seen: nothing to compare with
			} else {
				next.SIMChanges++
				next.RiskScore += e.policy.RiskSIMChange
				next.SIMHash = observation.SIMHash
				events = append(events, e.event(subject, EventSIMChanged, "a different SIM is in the device", "gateway", "risk raised"))
				e.downgradeLocked(&next, &events, "SIM changed")
			}
		}
		// The same identity key reporting a different hardware identifier: the
		// storage that holds the key was moved to, or kept through the
		// replacement of, the radio module. Handled exactly like a SIM change,
		// with its own weight. An absent value changes nothing and is not
		// remembered as "none": a device that stops reporting and later
		// reports a different value is still caught.
		if observation.HardwareHash != "" && next.HardwareHash != observation.HardwareHash {
			if next.HardwareHash == "" {
				next.HardwareHash = observation.HardwareHash // first value seen: recorded without penalty
			} else {
				next.ModemChanges++
				next.RiskScore += e.policy.RiskModemChange
				next.HardwareHash = observation.HardwareHash
				events = append(events, e.event(subject, EventModemChanged, "the device reports a different hardware identifier", "gateway", "risk raised"))
				e.downgradeLocked(&next, &events, "radio module changed")
			}
		}
	}

	if len(events) == 0 && observation.Healthy && observation.Elapsed > 0 {
		step := min(observation.Elapsed, e.policy.MaxAccrualStep)
		seconds := uint64(step / time.Second)
		switch next.State {
		case Observation:
			e.pending[subject] += seconds
			if next.StableSeconds+e.pending[subject] >= next.RequiredSeconds && !next.Manual {
				next.StableSeconds, e.pending[subject] = next.RequiredSeconds, 0
				next.State, next.Reason, next.TrustedSeconds = Trusted, "", 0
				events = append(events, e.event(subject, EventUpgrade, "the observation period completed without a change", "gateway", "trusted"))
			}
		case Trusted:
			next.TrustedSeconds += seconds
			if every := uint64(e.policy.RiskDecayEvery / time.Second); every > 0 && next.RiskScore > 0 && next.TrustedSeconds >= every {
				next.TrustedSeconds -= every
				next.RiskScore--
				return e.commitLocked(ctx, &next, nil)
			}
		}
	}
	if len(events) > 0 {
		return e.commitLocked(ctx, &next, events)
	}
	if e.pending[subject] >= uint64(e.flushEvery/time.Second) || next.SIMHash != record.SIMHash || next.HardwareHash != record.HardwareHash {
		return e.commitLocked(ctx, &next, nil)
	}
	// Trusted time accrues in memory and is written with the next change; it
	// only ever lowers the risk score, so losing some on a restart is safe.
	record.TrustedSeconds = next.TrustedSeconds
	return nil
}

// downgradeLocked restarts observation with a window chosen by the risk score,
// or quarantines at the top of the scale. It never shortens a restriction an
// administrator placed.
func (e *Engine) downgradeLocked(record *Record, events *[]Event, cause string) {
	previous := record.State
	e.pending[record.SubjectID] = 0
	record.StableSeconds, record.TrustedSeconds = 0, 0
	record.RequiredSeconds = uint64(e.policy.window(record.RiskScore) / time.Second)
	switch {
	case record.RiskScore >= e.policy.QuarantineAt:
		record.State, record.Reason = Quarantine, cause+"; repeated risk events - an administrator must review"
	case record.State == Quarantine || record.Manual:
		record.Reason = strings.TrimSpace(record.Reason + "; " + cause)
	default:
		record.State, record.Reason = Observation, cause+": observation period restarted"
	}
	if record.State != previous || previous != Observation {
		*events = append(*events, e.event(record.SubjectID, EventDowngrade, cause, "gateway",
			fmt.Sprintf("%s -> %s, observation %s", previous, record.State, time.Duration(record.RequiredSeconds)*time.Second)))
	}
}

// IntegrityFailure records evidence that the device's software failed its own
// verification (for example a release that no longer matched its signed
// manifest at start).
func (e *Engine) IntegrityFailure(ctx context.Context, subject, cause string) error {
	return e.change(ctx, subject, func(record *Record, events *[]Event) error {
		record.IntegrityFailures++
		record.RiskScore += e.policy.RiskIntegrityFailure
		*events = append(*events, e.event(subject, EventIntegrityFailure, cause, "device report", "risk raised"))
		if record.RiskScore >= e.policy.QuarantineAt {
			e.downgradeLocked(record, events, "integrity failure")
			return nil
		}
		previous := record.State
		if rank(record.State) < rank(Restricted) {
			record.State = Restricted
		}
		record.Reason = "integrity failure: " + Sanitize(cause)
		*events = append(*events, e.event(subject, EventDowngrade, "integrity failure", "gateway", fmt.Sprintf("%s -> %s", previous, record.State)))
		return nil
	})
}

// ObserveOTP folds the number of verification codes a subject received in the
// last 24 hours into its record. Crossing a threshold is recorded once, when
// it is crossed, not on every observation.
func (e *Engine) ObserveOTP(ctx context.Context, subject string, last24h int) error {
	level := 0
	switch {
	case last24h > e.policy.OTPHigh:
		level = 3
	case last24h > e.policy.OTPLimit:
		level = 2
	case last24h > e.policy.OTPNotice:
		level = 1
	}
	e.mu.Lock()
	record, known := e.records[subject]
	unchanged := !known || record.OTPLevel == level
	e.mu.Unlock()
	if unchanged {
		return nil
	}
	return e.change(ctx, subject, func(record *Record, events *[]Event) error {
		previous := record.OTPLevel
		record.OTPLevel = level
		record.OTPLimited = level >= 2
		if level < previous {
			if previous >= 2 && level < 2 {
				*events = append(*events, e.event(subject, EventOTPCleared, "verification codes in 24 hours fell below the limit", "gateway", "code forwarding resumed"))
			}
			return nil
		}
		result := "noted"
		switch level {
		case 2:
			result = "code forwarding stopped until the count falls"
		case 3:
			result = "code forwarding stopped; outgoing use restricted"
		}
		record.RiskScore += e.policy.RiskOTP
		*events = append(*events, e.event(subject, EventOTPLimit,
			fmt.Sprintf("more than %d verification codes in 24 hours", []int{0, e.policy.OTPNotice, e.policy.OTPLimit, e.policy.OTPHigh}[level]), "gateway", result))
		if level == 3 && rank(record.State) < rank(Restricted) {
			previousState := record.State
			record.State, record.Reason = Restricted, "an unusual number of verification codes"
			*events = append(*events, e.event(subject, EventDowngrade, "verification code volume", "gateway", fmt.Sprintf("%s -> %s", previousState, record.State)))
		}
		if record.RiskScore >= e.policy.QuarantineAt {
			e.downgradeLocked(record, events, "verification code volume")
		}
		return nil
	})
}

func (e *Engine) change(ctx context.Context, subject string, apply func(*Record, *[]Event) error) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	record, known := e.records[strings.TrimSpace(subject)]
	if !known {
		return ErrUnknownSubject
	}
	next := *record
	var events []Event
	if err := apply(&next, &events); err != nil {
		return err
	}
	return e.commitLocked(ctx, &next, events)
}

// ErrUnknownSubject reports a subject with no trust record.
var ErrUnknownSubject = errors.New("no trust record for this device")

// ErrNotAllowed reports an administrator action the rules do not permit.
var ErrNotAllowed = errors.New("not allowed")

// Restrict is an administrator stopping outgoing use of a device.
func (e *Engine) Restrict(ctx context.Context, subject, actor, reason string, quarantine bool) error {
	return e.change(ctx, subject, func(record *Record, events *[]Event) error {
		target, kind := Restricted, EventManualRestrict
		if quarantine {
			target, kind = Quarantine, EventManualQuarantine
		}
		previous := record.State
		record.State, record.Manual = MostRestrictive(record.State, target), true
		record.Reason = "restricted by an administrator: " + Sanitize(reason)
		e.pending[record.SubjectID] = 0
		record.StableSeconds = 0
		*events = append(*events, e.event(subject, kind, reason, "administrator "+actor, fmt.Sprintf("%s -> %s", previous, record.State)))
		return nil
	})
}

// Restore is an administrator lifting a restriction. It leads to OBSERVATION,
// never to TRUSTED: trust is earned again, over a window set by the risk the
// record has accumulated. A record whose signature or chain link failed is
// re-signed by this, which is the authorised recovery for a damaged record -
// and is why it is journalled like any other.
func (e *Engine) Restore(ctx context.Context, subject, actor, reason string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	record, known := e.records[strings.TrimSpace(subject)]
	if !known {
		return ErrUnknownSubject
	}
	damage, damaged := e.damaged[record.SubjectID]
	if !damaged && !record.Manual && record.State != Restricted && record.State != Quarantine {
		return fmt.Errorf("%w: the device is %s; there is nothing to restore", ErrNotAllowed, record.State)
	}
	next := *record
	var events []Event
	if damaged {
		// Counters in a record that failed verification are not believed, so
		// they are not carried forward as if they were; the risk is set to the
		// level that gives the longest window instead.
		events = append(events, e.event(subject, EventRecordDamaged, damage, "gateway", "record rebuilt on an administrator's request"))
		next.RiskScore = max(next.RiskScore, e.policy.LongestAt)
	}
	previous := next.State
	next.State, next.Manual, next.OTPLevel, next.OTPLimited = Observation, false, 0, false
	next.StableSeconds, next.TrustedSeconds = 0, 0
	e.pending[next.SubjectID] = 0
	next.RequiredSeconds = uint64(e.policy.window(next.RiskScore) / time.Second)
	next.Reason = "restored by an administrator: observation period"
	events = append(events, e.event(subject, EventManualRestore, reason, "administrator "+actor,
		fmt.Sprintf("%s -> %s, observation %s", previous, next.State, time.Duration(next.RequiredSeconds)*time.Second)))
	return e.commitLocked(ctx, &next, events)
}

// Grandfather records subjects that were paired before trust records existed
// as TRUSTED, once. Calls and messages already worked through them; starting
// them in observation would take that away on an upgrade. It does nothing
// after the first run, so a device adopted later is observed like any other.
func (e *Engine) Grandfather(ctx context.Context, subjects map[string]string) error {
	const key = "grandfathered"
	if _, done, err := e.store.Meta(ctx, key); err != nil || done {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := make([]string, 0, len(subjects))
	for id := range subjects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if _, exists := e.records[id]; exists || subjects[id] == "" {
			continue
		}
		record := Record{SubjectID: id, State: Trusted, BindingHash: subjects[id]}
		if err := e.commitLocked(ctx, &record, []Event{e.event(id, EventGrandfathered,
			"the device was paired before trust records existed", "gateway upgrade", "trusted without an observation period")}); err != nil {
			return err
		}
	}
	return e.store.SetMeta(ctx, key, e.now().UTC().Format(time.RFC3339))
}

// effectiveLocked is the state decisions are made on: the record's, unless the
// record or the journal cannot be believed.
func (e *Engine) effectiveLocked(subject string) (State, string) {
	record, known := e.records[subject]
	if !known {
		return Unbound, "the device has no trust record yet"
	}
	if reason, bad := e.damaged[subject]; bad {
		return MostRestrictive(record.State, Restricted), reason
	}
	if e.chainSet && !e.chain.OK {
		return MostRestrictive(record.State, Restricted), "the security journal failed verification: " + e.chain.Problem
	}
	return record.State, record.Reason
}

func allowed(state State, otpLimited bool, action Action) bool {
	switch action {
	case ActionUpdate, ActionDiagnose:
		return true
	case ActionReceiveCall, ActionReceiveSMS:
		return state == Observation || state == Trusted || state == Restricted
	case ActionForwardOTP:
		return (state == Observation || state == Trusted) && !otpLimited
	case ActionDial, ActionSendSMS, ActionDTMF:
		return state == Trusted
	}
	return false
}

// Check reports whether an action is allowed, without recording anything.
func (e *Engine) Check(subject string, action Action) (bool, State, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	subject = strings.TrimSpace(subject)
	state, reason := e.effectiveLocked(subject)
	limited := false
	if record, known := e.records[subject]; known {
		limited = record.OTPLimited
	}
	if action == ActionForwardOTP && limited && reason == "" {
		reason = "an unusual number of verification codes"
	}
	if reason == "" && state == Observation {
		reason = "observation period"
	}
	return allowed(state, limited, action), state, reason
}

// Require is the gate callers put in front of an action. In Enforce mode it
// returns *ErrRefused when the action is not allowed. In Monitor mode it
// returns nil and records what would have been refused. Refusals are
// journalled at most once per RefusalEventEvery per subject and action.
func (e *Engine) Require(ctx context.Context, subject string, action Action) error {
	ok, state, reason := e.Check(subject, action)
	if ok {
		return nil
	}
	subject = strings.TrimSpace(subject)
	e.mu.Lock()
	key := subject + "\x1f" + string(action)
	last, seen := e.refused[key]
	due := !seen || e.now().Sub(last) >= e.policy.RefusalEventEvery
	if due {
		e.refused[key] = e.now()
		result := "refused"
		if e.enforcement == Monitor {
			result = "would be refused; monitor mode, not enforced"
		}
		// Appended without a record change; a failure to journal must not turn
		// a refusal into a permission, so the error is not consulted below.
		_ = e.store.Commit(ctx, nil, []Event{e.event(subject, EventActionRefused, fmt.Sprintf("%s while %s", action, state), "gateway", result)}, e.seal)
	}
	e.mu.Unlock()
	if e.enforcement == Monitor {
		return nil
	}
	return &ErrRefused{SubjectID: subject, Action: action, State: state, Reason: reason}
}

// SubjectView is one subject as an administrator sees it.
type SubjectView struct {
	Record
	// EffectiveState is what decisions use. It differs from State when the
	// record or the journal failed verification.
	EffectiveState   State           `json:"effective_state"`
	EffectiveReason  string          `json:"effective_reason,omitempty"`
	RecordVerified   bool            `json:"record_verified"`
	RemainingSeconds uint64          `json:"remaining_seconds"`
	Allowed          map[Action]bool `json:"allowed"`
}

// Subjects lists every subject.
func (e *Engine) Subjects() []SubjectView {
	e.mu.Lock()
	defer e.mu.Unlock()
	views := make([]SubjectView, 0, len(e.records))
	for id, record := range e.records {
		view := SubjectView{Record: *record, Allowed: map[Action]bool{}}
		view.StableSeconds += e.pending[id]
		view.EffectiveState, view.EffectiveReason = e.effectiveLocked(id)
		_, bad := e.damaged[id]
		view.RecordVerified = !bad
		if view.State == Observation && view.RequiredSeconds > view.StableSeconds {
			view.RemainingSeconds = view.RequiredSeconds - view.StableSeconds
		}
		for _, action := range Actions {
			view.Allowed[action] = allowed(view.EffectiveState, record.OTPLimited, action)
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].SubjectID < views[j].SubjectID })
	return views
}

// Events returns journal entries after a sequence number, oldest first.
func (e *Engine) Events(ctx context.Context, afterSeq int64, limit int, subject string) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return e.store.Events(ctx, afterSeq, limit, strings.TrimSpace(subject))
}

// RecentEvents returns journal entries older than a sequence number, newest
// first; beforeSeq <= 0 starts at the newest entry.
func (e *Engine) RecentEvents(ctx context.Context, beforeSeq int64, limit int, subject string) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return e.store.EventsBefore(ctx, beforeSeq, limit, strings.TrimSpace(subject))
}

// KeyedHash makes the hashes observations carry: HMAC-like, keyed by the
// Core's public identity so the same SIM hashes differently on another
// gateway, and one-way so the journal never holds the identifier.
func KeyedHash(corePublic ed25519.PublicKey, label string, parts ...string) string {
	digest := sha256.New()
	digest.Write([]byte("nsh-trust-hash-v1\x00" + label + "\x00"))
	digest.Write([]byte(base64.StdEncoding.EncodeToString(corePublic)))
	for _, part := range parts {
		digest.Write([]byte{0})
		digest.Write([]byte(part))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// Event kinds recorded on behalf of other parts of the gateway.
const (
	EventIdentityVerification = "IDENTITY_VERIFICATION"
	EventUpdateApplied        = "AGENT_UPDATE"
)

// Note appends an event that changes no trust state: something another part
// of the gateway established and that belongs in the same journal (an identity
// verification outcome, an agent update). Text is sanitised like every event.
func (e *Engine) Note(ctx context.Context, subject, kind, cause, source, result string) error {
	if strings.TrimSpace(kind) == "" {
		return errors.New("an event needs a kind")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.store.Commit(ctx, nil, []Event{e.event(strings.TrimSpace(subject), kind, cause, source, result)}, e.seal)
}
