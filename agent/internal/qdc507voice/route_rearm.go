package qdc507voice

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

// Re-arming the module's VoLTE route session after every call.
//
// WHAT GOES WRONG WITHOUT THIS. mavo-pcm-bridge --voice-route-session opens
// hw:0,4 and stays resident, but the session it establishes is ONE-SHOT: it
// carries the first call and after that the DSP stops writing to that device
// entirely. Both directions go silent, on every subsequent call, incoming and
// outgoing alike.
//
// And nothing shows it. The bridge process is still alive, its log still says
// "VoLTE route session active on hw:0,4", /sys/class/android_usb/f_audio/
// audio_enable is still 1, and pcm4p/pcm4c still read state: RUNNING. Those are
// exactly the five things routeReady checks, so ensureLine's ready
// short-circuit returns success and the route is never restarted. From the
// outside the module reports a perfectly healthy voice path that carries
// nothing.
//
// This is not our measurement. It comes from cellbridge-mac
// (github.com/easonjoo/cellbridge-mac), which drives the SAME BINARY -- its
// mavo-pcm-bridge.armv7 is sha256 88d47c15e61d1428a59c821fed804c2e6490e828
// 59a085062f21966b58d167fc, byte for byte the artifact in expectedArtifacts,
// both fetched from moluncn/mavo -- on the same module and the same kernel
// modules. Their control experiment: change nothing on the host, restart only
// this bridge, and the next call recovers (measured 2060 and 2150 nonzero
// samples, mean ~837, against nonzero=0 mean=0 before). Recorded here because
// "restarting a healthy-looking process fixes the audio" is the kind of claim
// that gets optimised away by the next reader.
//
// THE ONE RULE, which that project arrived at after four failed attempts and
// which is worth more than the code below: never skip a re-arm because a call
// is in progress or because one just started. Skipping means that call is
// silent. There is no such thing as a busy moment worth protecting -- a dirty
// bridge carries nothing, so the worst a re-arm during setup can do is nothing,
// and the best is the whole call.
//
// WHERE THIS DIFFERS FROM THEIRS. cellbridge watches its gateway's log file and
// pairs "call started" and "call ended" lines, which forced a coalescing window
// to suppress duplicate end-of-call reports and a fallback for dropped lines.
// We do not need any of that: the agent already projects call state on every
// snapshot, so ObserveCallActivity sees the transition itself rather than a
// report of it, and gets exactly one per call. The state machine is
// correspondingly smaller, and deliberately so -- the coalescing window was the
// source of their remaining bug (a call shorter than the window lost its
// re-arm, which silenced the call after it).

// routeRearmTimeout bounds one stop-and-start cycle. Generous because it spans
// a SIGTERM wait, a possible SIGKILL, and a fresh readiness poll over the
// single ADB channel that the rest of the runtime also shares.
const routeRearmTimeout = 40 * time.Second

// routeRearmDelay is how long to wait after a call ends before re-arming. The
// module needs a moment to tear the old session down; re-arming into the
// teardown produces a bridge that reports itself active against a session the
// DSP is still dismantling.
const routeRearmDelay = 2 * time.Second

// routeRearmBackoffBase is the wait before the first retry after a failed
// re-arm; each consecutive failure doubles it up to routeRearmBackoffMax. A
// failing re-arm used to retry on literally the next snapshot (~3s), which
// hammered the one ADB channel everything else shares.
const routeRearmBackoffBase = 15 * time.Second

// routeRearmBackoffMax caps the doubling. Five consecutive failures means
// something structural is wrong (module gone, runtime broken) and only a
// human or a new call should try again.
const routeRearmBackoffMax = 4 * time.Minute

// helperPattern turns the helper path into a pkill -f pattern that cannot
// match the shell carrying the pkill itself. The path appears verbatim in that
// shell's command line -- pkill -f matches against the whole command line -- so
// the shell used to SIGTERM itself before it could report an exit status,
// making every stop fail with "did not contain an exit status" and the retry
// loop kill the bridge it had just been asked to replace. Bracketing one
// character (and escaping the dots) keeps the regex pointing at the bridge
// while the pattern's own text no longer matches it -- the same trick as
// grep '[b]ridge'.
func helperPattern(helper string) string {
	escaped := strings.ReplaceAll(helper, ".", "[.]")
	return strings.Replace(escaped, "bridge", "bridg[e]", 1)
}

// routeArmState tracks one line's bridge across calls.
type routeArmState struct {
	// inCall is the last observed call presence, so transitions can be
	// detected rather than levels acted on repeatedly.
	inCall bool
	// dirty means the bridge has carried a call and has not been re-armed
	// since, so the next call on it would be silent.
	dirty bool
	// arming means a re-arm is already running for this line. Without it a
	// snapshot every second would pile up concurrent restarts, each killing
	// the bridge the previous one had just started.
	arming bool
	// attempts counts consecutive failed re-arms for the backoff, and
	// nextAttempt is when the next retry becomes eligible. Reset when a
	// re-arm succeeds or a new call starts (THE ONE RULE: a new call on a
	// dirty bridge always retries immediately).
	attempts    int
	nextAttempt time.Time
}

// routeArming is the per-line arming state, separate from the reconciler's own
// state map because it is written from the snapshot path rather than the
// reconcile loop.
type routeArming struct {
	mu     sync.Mutex
	states map[string]*routeArmState
}

func (a *routeArming) state(key string) *routeArmState {
	if a.states == nil {
		a.states = make(map[string]*routeArmState)
	}
	state, found := a.states[key]
	if !found {
		state = &routeArmState{}
		a.states[key] = state
	}
	return state
}

// ObserveCallActivity records whether a line currently has any live call, and
// re-arms the route session when that changes in a way that leaves the bridge
// unable to carry the next call.
//
// It returns immediately: the re-arm itself takes seconds of ADB traffic and
// this is called from the snapshot projection, which must not block on the
// module. Failures are logged and leave the line marked dirty, so the next
// observation retries rather than the fault being swallowed.
func (m *Manager) ObserveCallActivity(line domain.Line, active bool) {
	if !m.Enabled() || line.PhysicalDevice == "" {
		return
	}
	key := line.PhysicalDevice

	m.arming.mu.Lock()
	state := m.arming.state(key)
	reason := ""
	switch {
	case active && !state.inCall:
		state.inCall = true
		// THE ONE RULE: a new call always retries immediately, whatever the
		// backoff owed on previous failures -- silence for that call is the
		// alternative.
		state.attempts = 0
		state.nextAttempt = time.Time{}
		if state.dirty {
			// A call has started on a bridge that has already been used. It
			// would carry no audio, so restart it now even though the call is
			// in progress -- see THE ONE RULE above.
			reason = "call started on a route session that has already been used"
		}
	case !active && state.inCall:
		state.inCall = false
		state.dirty = true
		state.attempts = 0
		state.nextAttempt = time.Time{}
		reason = "a call ended, so the route session has been consumed"
	case state.dirty && !state.arming:
		// A previous re-arm failed. Retry on the next observation, but spaced
		// out by the backoff -- the previous behaviour retried on literally
		// the next snapshot (~3s), hammering the one ADB channel the rest of
		// the runtime shares.
		if !state.nextAttempt.IsZero() && time.Now().Before(state.nextAttempt) {
			m.arming.mu.Unlock()
			return
		}
		reason = "an earlier re-arm of the route session did not succeed"
	}
	if reason == "" || state.arming {
		m.arming.mu.Unlock()
		return
	}
	state.arming = true
	m.arming.mu.Unlock()

	go m.rearmInBackground(line, key, reason, !active)
}

func (m *Manager) rearmInBackground(line domain.Line, key, reason string, idle bool) {
	defer func() {
		m.arming.mu.Lock()
		m.arming.state(key).arming = false
		m.arming.mu.Unlock()
	}()

	if idle {
		// Only the after-a-call path waits. A call in progress is already
		// silent, so the delay would be two seconds of silence bought for
		// nothing.
		delay := m.rearmDelay
		if delay <= 0 {
			delay = routeRearmDelay
		}
		time.Sleep(delay)
	}
	ctx, cancel := context.WithTimeout(context.Background(), routeRearmTimeout)
	defer cancel()

	if err := m.RearmRoute(ctx, line); err != nil {
		// Back off before the retry that the next observation will schedule:
		// double from the base up to the cap. Nothing here resets dirty -- the
		// retry itself is the next observation past nextAttempt.
		m.arming.mu.Lock()
		state := m.arming.state(key)
		state.attempts++
		base := m.rearmBackoff
		if base <= 0 {
			base = routeRearmBackoffBase
		}
		backoff := base
		for round := 1; round < state.attempts; round++ {
			backoff *= 2
			if backoff >= routeRearmBackoffMax {
				backoff = routeRearmBackoffMax
				break
			}
		}
		state.nextAttempt = time.Now().Add(backoff)
		m.arming.mu.Unlock()
		slog.Warn(
			"re-arm QDC507 VoLTE route session",
			"component", "qdc507voice",
			"line_id", line.ID,
			"reason", reason,
			"error", err,
			"attempt", state.attempts,
			"next_retry_in", backoff.String(),
		)
		return
	}
	m.arming.mu.Lock()
	state := m.arming.state(key)
	state.dirty = false
	state.attempts = 0
	state.nextAttempt = time.Time{}
	m.arming.mu.Unlock()
	slog.Info(
		"re-armed the QDC507 VoLTE route session",
		"component", "qdc507voice",
		"line_id", line.ID,
		"reason", reason,
	)
}

// RearmRoute stops the resident bridge and starts a fresh one.
//
// Exported so the settings page and the audio probe can force it: "the second
// call onward is silent" is a report that arrives long before the state machine
// above is trusted, and an operator needs a button that does exactly this.
func (m *Manager) RearmRoute(ctx context.Context, line domain.Line) error {
	if !m.Enabled() {
		return fmt.Errorf("QDC507 voice runtime is not configured")
	}
	device, err := m.findDevice(ctx, line.PhysicalDevice)
	if err != nil {
		return err
	}
	if err := m.stopRoute(ctx, device.Selector); err != nil {
		return err
	}
	return m.startRouteSession(ctx, device.Selector)
}

// stopRoute ends the resident bridge, by name rather than by pidfile.
//
// By name because the pidfile can be stale or absent -- a bridge left behind by
// an earlier agent build, or by the vendor app, owns the DSP session just as
// effectively as one we started, and leaving it running means the new instance
// finds the device busy. SIGTERM first so it can release hw:0,4 cleanly, then
// SIGKILL, because a bridge that will not exit is worse than one killed hard:
// the next start fails and the line stays silent.
func (m *Manager) stopRoute(ctx context.Context, serial string) error {
	pattern := helperPattern(remoteDirectory + "/" + m.manifest.Helper)
	command := "pkill -TERM -f '" + pattern + "' 2>/dev/null; " +
		"n=0; while pidof " + m.manifest.Helper + " >/dev/null 2>&1 && test \"$n\" -lt 40; do " +
		"sleep 0.1; n=$((n+1)); done; " +
		"if pidof " + m.manifest.Helper + " >/dev/null 2>&1; then " +
		"pkill -KILL -f '" + pattern + "' 2>/dev/null; " +
		"n=0; while pidof " + m.manifest.Helper + " >/dev/null 2>&1 && test \"$n\" -lt 20; do " +
		"sleep 0.1; n=$((n+1)); done; fi; " +
		"rm -f '" + routePIDFile + "'; " +
		"! pidof " + m.manifest.Helper + " >/dev/null 2>&1"
	result, err := m.client.Shell(ctx, serial, command, 15*time.Second)
	if err != nil || result.Status != 0 {
		return shellFailure("stop the QDC507 VoLTE route session", result, err)
	}
	return nil
}

// startRouteSession launches a bridge and waits for it to report its session
// active.
//
// It waits on the LOOSE readiness condition -- the process is alive and has
// logged "VoLTE route session active on hw:0,4" -- not the strict one
// routeReady uses. The strict check also requires audio_enable=1 and both PCM
// substreams in state RUNNING, and those only become true once the host opens
// the USB audio stream, which at idle it has not. Waiting for them here would
// time out on a bridge that is working perfectly and report a failure for the
// normal case.
func (m *Manager) startRouteSession(ctx context.Context, serial string) error {
	helper := remoteDirectory + "/" + m.manifest.Helper
	command := "rm -f '" + routePIDFile + "' '" + routeLogFile + "'; " +
		"nohup '" + helper + "' --voice-route-session --verbose " +
		"</dev/null >> '" + routeLogFile + "' 2>&1 & pid=$!; " +
		"starttime=$(cut -d ' ' -f 22 \"/proc/$pid/stat\" 2>/dev/null); " +
		"case \"$pid:$starttime\" in :*|*:|*[!0-9:]*) false;; *) " +
		"printf '%s %s\\n' \"$pid\" \"$starttime\" > '" + routePIDFile + "';; esac"
	if result, err := m.client.Shell(ctx, serial, command, 8*time.Second); err != nil || result.Status != 0 {
		return shellFailure("start the QDC507 VoLTE route session", result, err)
	}

	deadline := time.NewTimer(m.deviceWait)
	defer deadline.Stop()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	for {
		active, err := m.routeSessionActive(ctx, serial)
		if err == nil && active {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			detail, _, _ := m.shellOutput(
				ctx,
				serial,
				"test ! -f '"+routeLogFile+"' || tail -n 80 '"+routeLogFile+"'",
				8*time.Second,
			)
			return fmt.Errorf(
				"the restarted QDC507 VoLTE route session never reported itself active%s",
				suffixDetail(detail),
			)
		case <-ticker.C:
		}
	}
}

// routeSessionActive is the loose readiness check described on
// startRouteSession: the bridge we started is the process that is running, and
// it has claimed the device.
func (m *Manager) routeSessionActive(ctx context.Context, serial string) (bool, error) {
	helper := remoteDirectory + "/" + m.manifest.Helper
	command := "test -s '" + routePIDFile + "' && " +
		"read pid expected_start < '" + routePIDFile + "' && " +
		"test \"$(cut -d ' ' -f 22 \"/proc/$pid/stat\" 2>/dev/null)\" = \"$expected_start\" && " +
		"test \"$(tr '\\000' '\\n' < \"/proc/$pid/cmdline\" 2>/dev/null | sed -n '1p')\" = '" + helper + "' && " +
		"tr '\\000' '\\n' < \"/proc/$pid/cmdline\" 2>/dev/null | grep -q '^--voice-route-session$' && " +
		"grep -q 'VoLTE route session active on hw:0,4' '" + routeLogFile + "'"
	result, err := m.client.Shell(ctx, serial, command, 8*time.Second)
	if err != nil {
		return false, err
	}
	return result.Status == 0, nil
}
