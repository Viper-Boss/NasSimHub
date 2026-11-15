package airsimgw

import (
	"context"
	"errors"
	"time"
)

// Identity is who a verified AirSIM request acts as: the NasSimHub account
// that paired the iPhone, limited to the one line the gateway exposes.
type Identity struct {
	PairingID string
	UserID    string
	LineID    string
	LineIDs   []string
	Peer      string
}

// Line is what the app shows as "the phone" it is paired with.
type Line struct {
	DeviceName   string
	Manufacturer string
	Model        string
	Firmware     string
	PhoneNumber  string
	Operator     string
	IMEI         string
	ICCID        string
	IMSI         string
	SignalDBM    *int
	Registered   bool
	Searching    bool
	NetworkMode  string
	Band         string
}

// Call phases are NasSimHub's: dialing, ringing, connecting, active, ending,
// ended, failed.
type Call struct {
	LineID         string
	SIMAccount     string
	ID             string
	Direction      string // incoming | outgoing
	Phase          string
	Number         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	EndedAt        time.Time
	Missed         bool
	MediaAvailable bool
}

type Message struct {
	LineID     string
	SIMAccount string
	DeliveryID string
	Sender     string
	Content    string
	Outgoing   bool
	Timestamp  time.Time
}

// PCMStream is one call's audio in the gateway's host format.
type PCMStream interface {
	SampleRate() int
	FrameBytes() int
	ReadDownlink(context.Context) ([]byte, error)
	WriteUplink(context.Context, []byte) error
	Close()
	Done() <-chan struct{}
}

// Backend is NasSimHub as seen by the gateway. The httpapi package implements
// it by running each command through the same handlers (line scoping, call
// ownership, anti-fraud guard) a signed-in browser goes through.
type Backend interface {
	// Authorize fails once the pairing's account is disabled, deleted or no
	// longer an administrator, or has lost the line.
	Authorize(context.Context, Identity) error
	Line(context.Context, Identity) (Line, error)
	// Calls returns the line's live calls and its most recent history.
	Calls(context.Context, Identity, int) (active []Call, history []Call, err error)
	Dial(context.Context, Identity, string, string) (Call, error)
	CallAction(context.Context, Identity, string, string, string, string) error
	// KeepCall renews the pairing's ownership of a call it controls.
	KeepCall(context.Context, Identity, string) error
	Messages(context.Context, Identity, time.Time, int) ([]Message, error)
	SendMessage(context.Context, Identity, string, string, string) error
	AttachPCM(context.Context, Identity, string) (PCMStream, error)
}

// Backend errors. Anything else is reported as a gateway error.
var (
	ErrForbidden   = errors.New("airsim: not allowed")
	ErrNotFound    = errors.New("airsim: not found")
	ErrConflict    = errors.New("airsim: conflict")
	ErrUnavailable = errors.New("airsim: unavailable")
)

// BackendError carries a message the app may show.
type BackendError struct {
	Kind    error
	Message string
}

func (e *BackendError) Error() string { return e.Message }
func (e *BackendError) Unwrap() error { return e.Kind }
