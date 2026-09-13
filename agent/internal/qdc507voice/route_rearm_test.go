package qdc507voice

import (
	"context"
	"io/fs"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

// rearmClient models the module closely enough to tell a stop-then-start apart
// from a no-op, and refuses the STRICT readiness condition so that a re-arm
// which waits on it cannot pass.
type rearmClient struct {
	mu      sync.Mutex
	steps   []string
	running bool
	// startFails makes the bridge refuse to come up, for the retry case.
	startFails bool
}

func (c *rearmClient) Devices(context.Context) ([]Device, error) {
	return []Device{{Selector: "transport:2", State: "device", USB: "usb:1-10"}}, nil
}

func (c *rearmClient) Shell(
	_ context.Context,
	_ string,
	command string,
	_ time.Duration,
) (ShellResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case strings.Contains(command, "pkill -TERM -f"):
		c.steps = append(c.steps, "stop")
		c.running = false
		return ShellResult{}, nil
	case strings.Contains(command, "nohup '") && strings.Contains(command, "--voice-route-session"):
		c.steps = append(c.steps, "start")
		c.running = !c.startFails
		return ShellResult{}, nil
	case strings.Contains(command, "audio_enable"):
		// The strict readiness condition. At idle the host has not opened the
		// USB audio stream, so this is false on a perfectly working bridge --
		// which is why the re-arm must not wait for it.
		return ShellResult{Status: 1}, nil
	case strings.Contains(command, "VoLTE route session active on hw:0,4"):
		if c.running {
			return ShellResult{}, nil
		}
		return ShellResult{Status: 1}, nil
	default:
		return ShellResult{}, nil
	}
}

func (c *rearmClient) Push(
	context.Context, string, string, string, fs.FileMode, time.Duration,
) error {
	return nil
}

func (c *rearmClient) sequence() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.steps...)
}

// waitForSteps gives the background re-arm a bounded moment to finish.
// ObserveCallActivity returns immediately by design, so the assertion has to
// wait for the goroutine rather than read the state straight after.
func (c *rearmClient) waitForSteps(t *testing.T, want int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		steps := c.sequence()
		if len(steps) >= want {
			return steps
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %d module steps, saw %v", want, steps)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func rearmManager(client Client) *Manager {
	manager := testManager(client)
	// The production delay is two seconds of module teardown. Shortened here so
	// the suite is not dominated by it; the delay's own purpose is timing on
	// real hardware and nothing in these tests depends on its length.
	manager.rearmDelay = time.Millisecond
	return manager
}

// A call ending must restart the session, because the one it used is spent.
func TestACallEndingRearmsTheRouteSession(t *testing.T) {
	t.Parallel()
	client := &rearmClient{running: true}
	manager := rearmManager(client)
	line := testLine()

	manager.ObserveCallActivity(line, true)
	if steps := client.sequence(); len(steps) != 0 {
		t.Fatalf("a call starting on a fresh session restarted it: %v", steps)
	}
	manager.ObserveCallActivity(line, false)

	steps := client.waitForSteps(t, 2)
	if steps[0] != "stop" || steps[1] != "start" {
		t.Fatalf(
			"a call ended and the session was not stopped then started: %v; "+
				"every call after this one carries no audio in either direction",
			steps,
		)
	}
}

// The re-arm must not wait on the strict readiness condition.
//
// This is the subtle one. routeReady also requires audio_enable=1 and both PCM
// substreams in state RUNNING, and neither is true until the host opens the USB
// audio stream -- which at idle, right after a call ended, it has not. A re-arm
// that waits for them times out on a healthy bridge and reports a failure for
// the normal case, leaving the line marked dirty forever. The fake above
// answers the strict check with a failure, so reusing routeReady here fails
// this test.
func TestRearmingSucceedsBeforeTheHostOpensTheAudioStream(t *testing.T) {
	t.Parallel()
	client := &rearmClient{running: true}
	manager := rearmManager(client)
	if err := manager.RearmRoute(context.Background(), testLine()); err != nil {
		t.Fatalf(
			"RearmRoute() error = %v; the session was restarted correctly and "+
				"reported a failure because it waited for a condition only a "+
				"call in progress satisfies",
			err,
		)
	}
}

// A call starting on a session that has already been used must restart it, even
// though a call is in progress.
//
// The rule this guards is the one cellbridge-mac arrived at after four failed
// attempts: skipping a re-arm because the line is busy means that call is
// silent. A dirty session carries nothing, so there is no audio to protect.
func TestACallStartingOnAUsedSessionRearmsImmediately(t *testing.T) {
	t.Parallel()
	client := &rearmClient{running: true}
	manager := rearmManager(client)
	line := testLine()

	// First call, then its end -- which re-arms.
	manager.ObserveCallActivity(line, true)
	manager.ObserveCallActivity(line, false)
	client.waitForSteps(t, 2)

	// Make the session dirty again without letting the end-of-call re-arm
	// clean it up, which is what a re-arm failure looks like.
	manager.arming.mu.Lock()
	manager.arming.state(line.PhysicalDevice).dirty = true
	manager.arming.mu.Unlock()

	manager.ObserveCallActivity(line, true)
	steps := client.waitForSteps(t, 4)
	if steps[2] != "stop" || steps[3] != "start" {
		t.Fatalf(
			"a call started on a used session and it was not restarted: %v; "+
				"that call is silent in both directions",
			steps,
		)
	}
}

// Nothing may happen on a line that has never had a call. Restarting the
// session on every snapshot would tear down a working bridge once a second.
func TestAnIdleLineIsLeftAlone(t *testing.T) {
	t.Parallel()
	client := &rearmClient{running: true}
	manager := rearmManager(client)
	line := testLine()
	for range 5 {
		manager.ObserveCallActivity(line, false)
	}
	time.Sleep(50 * time.Millisecond)
	if steps := client.sequence(); len(steps) != 0 {
		t.Fatalf("an idle line was churned: %v", steps)
	}
}

// Concurrent observations must not pile up restarts. Each would kill the bridge
// the previous one had just started, and the line would never settle.
func TestOverlappingObservationsRearmOnce(t *testing.T) {
	t.Parallel()
	client := &rearmClient{running: true}
	manager := rearmManager(client)
	line := testLine()

	manager.ObserveCallActivity(line, true)
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			manager.ObserveCallActivity(line, false)
		}()
	}
	group.Wait()
	client.waitForSteps(t, 2)
	// Give any extra goroutine time to land a second cycle.
	time.Sleep(150 * time.Millisecond)
	if steps := client.sequence(); len(steps) != 2 {
		t.Fatalf("overlapping observations produced %v", steps)
	}
}

// A failed re-arm must be retried rather than leaving the line quietly broken.
// The retry is spaced out by the backoff, so the test winds the backoff clock
// forward by hand (deterministic, no sleeping on a real timer).
func TestAFailedRearmIsRetriedAfterTheBackoff(t *testing.T) {
	t.Parallel()
	client := &rearmClient{running: true, startFails: true}
	manager := rearmManager(client)
	line := testLine()

	manager.ObserveCallActivity(line, true)
	manager.ObserveCallActivity(line, false)
	client.waitForSteps(t, 2)

	// While the backoff has time left, observations must NOT retry.
	manager.arming.mu.Lock()
	manager.arming.state(line.PhysicalDevice).nextAttempt = time.Now().Add(time.Minute)
	manager.arming.mu.Unlock()
	manager.ObserveCallActivity(line, false)
	manager.ObserveCallActivity(line, false)
	if len(client.sequence()) != 2 {
		t.Fatalf(
			"observations during the backoff retried anyway: %v",
			client.sequence(),
		)
	}

	// The bridge can come up now; the backoff expires and the next
	// observation must try again.
	client.mu.Lock()
	client.startFails = false
	client.mu.Unlock()
	manager.arming.mu.Lock()
	manager.arming.state(line.PhysicalDevice).nextAttempt = time.Now().Add(-time.Second)
	manager.arming.mu.Unlock()

	deadline := time.Now().Add(3 * time.Second)
	for {
		// Keep the backoff expired: the first re-arm's own failure handler
		// runs late (after waitForSteps) and would otherwise re-arm the gate
		// for another 15s on top of the expired window this test sets.
		manager.arming.mu.Lock()
		manager.arming.state(line.PhysicalDevice).nextAttempt = time.Now().Add(-time.Second)
		manager.arming.mu.Unlock()
		manager.ObserveCallActivity(line, false)
		if len(client.sequence()) >= 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"a failed re-arm was never retried: %v; the line stays silent "+
					"with every health signal reporting a working path",
				client.sequence(),
			)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// And once it succeeds the line stops being retried.
	after := len(client.sequence())
	for range 5 {
		manager.ObserveCallActivity(line, false)
	}
	time.Sleep(100 * time.Millisecond)
	if len(client.sequence()) != after {
		t.Fatalf("a healthy line kept being restarted: %v", client.sequence())
	}
}

// A runtime that is not configured must not pretend to re-arm anything.
func TestRearmingRequiresAConfiguredRuntime(t *testing.T) {
	t.Parallel()
	manager, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RearmRoute(context.Background(), testLine()); err == nil {
		t.Fatal("RearmRoute() on an unconfigured runtime reported success")
	}
	// And the observation path must be a no-op rather than a panic.
	manager.ObserveCallActivity(testLine(), true)
	manager.ObserveCallActivity(testLine(), false)
}

// A line with no physical device cannot be addressed, so it must be ignored
// rather than sharing one arming slot under the empty key.
func TestALineWithoutAPhysicalDeviceIsIgnored(t *testing.T) {
	t.Parallel()
	client := &rearmClient{running: true}
	manager := rearmManager(client)
	line := domain.Line{ID: "line-qdc507"}
	manager.ObserveCallActivity(line, true)
	manager.ObserveCallActivity(line, false)
	time.Sleep(50 * time.Millisecond)
	if steps := client.sequence(); len(steps) != 0 {
		t.Fatalf("a line with no physical device was acted on: %v", steps)
	}
}

// The pkill pattern must keep matching the bridge process while no longer
// matching the shell that carries the pkill itself: pkill -f compares against
// the whole command line, and the path appears verbatim inside it, so an
// unbracketed pattern made the shell kill itself before it could report an
// exit status -- every stop failed, the bridge was killed anyway by the
// SIGTERM, and the retry loop never recovered the route.
func TestHelperPatternMatchesTheBridgeButNotItsOwnShell(t *testing.T) {
	// Built from the manifest rather than written out, because the escape is
	// only correct for a name that contains the substring it brackets. A
	// hardcoded path keeps this test passing while a renamed helper silently
	// goes back to killing its own shell -- which cost a day the first time.
	helper := remoteDirectory + "/" + expectedManifest.Helper
	pattern := helperPattern(helper)
	if pattern == helper {
		t.Fatalf(
			"helperPattern left %q unchanged, so it still matches the shell that "+
				"carries it; the escape does not cover this helper name",
			helper,
		)
	}

	bridgeMatch, err := regexp.MatchString(pattern, helper)
	if err != nil {
		t.Fatalf("pattern %q is not a valid regexp: %v", pattern, err)
	}
	if !bridgeMatch {
		t.Fatalf("pattern %q does not match the bridge cmdline %q", pattern, helper)
	}

	shellCmdline := "sh -c pkill -TERM -f '" + pattern + "' 2>/dev/null; n=0; while pidof mavo-pcm-bridge >/dev/null 2>&1; do sleep 0.1; done"
	shellMatch, err := regexp.MatchString(pattern, shellCmdline)
	if err != nil {
		t.Fatalf("pattern %q is not a valid regexp: %v", pattern, err)
	}
	if shellMatch {
		t.Fatalf("pattern %q matches the carrying shell's own cmdline and would kill it", pattern)
	}
}

// The property has to hold for ANY helper name, not just today's.
//
// The first version of helperPattern bracketed the literal substring "bridge"
// and escaped the dots, so it worked only because the helper happens to be
// called mavo-pcm-bridge.armv7. A rename to a name with no dot and no "bridge"
// in it turned the escape into a no-op and restored the self-kill, with every
// test still green. These cases are the ones that would have caught that.
func TestHelperPatternNeverMatchesItsOwnTextForAnyHelperName(t *testing.T) {
	for _, helper := range []string{
		"/tmp/modemdeck-qdc507-voice/mavo-pcm-bridge.armv7",
		// No dot in the name: dot escaping alone does nothing.
		"/tmp/modemdeck-qdc507-voice/mavo-pcm-bridge",
		// No "bridge" either: substring bracketing alone does nothing.
		"/tmp/modemdeck-qdc507-voice/pcmhelper",
		// Single-character name, the smallest thing the bracketing must handle.
		"/tmp/x/h",
	} {
		pattern := helperPattern(helper)
		matchesBridge, err := regexp.MatchString(pattern, helper)
		if err != nil {
			t.Fatalf("%s: pattern %q is not a valid regexp: %v", helper, pattern, err)
		}
		if !matchesBridge {
			t.Fatalf("%s: pattern %q no longer matches the bridge it must kill", helper, pattern)
		}
		carrier := "sh -c pkill -TERM -f '" + pattern + "'"
		matchesItself, err := regexp.MatchString(pattern, carrier)
		if err != nil {
			t.Fatalf("%s: %v", helper, err)
		}
		if matchesItself {
			t.Fatalf(
				"%s: pattern %q matches the shell carrying it, so the stop command "+
					"kills itself and every re-arm fails",
				helper,
				pattern,
			)
		}
	}
}
