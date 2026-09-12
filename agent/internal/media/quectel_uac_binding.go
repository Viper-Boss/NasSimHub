package media

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Deriving the media binding for a Quectel UAC line.
//
// WHY THIS EXISTS. A media binding maps an audio port to a backend and an
// endpoint, and until now bindings came ONLY from a JSON file passed to the
// agent with -media-bindings. That file is keyed by the audio port string, and
// for a Quectel module the port is "quectel-uac:" followed by the line's sysfs
// physical device path -- something like
//
//	quectel-uac:/sys/devices/pci0000:00/usb1/1-10
//
// Nobody can be expected to write that by hand, it changes when the module is
// plugged into a different socket, and the agent default for the file is empty.
// So on every real installation the bindings map was empty, IsConfigured
// answered false for every port, and the server's call projection -- which
// requires MediaConfigured before it will tell the browser that media is
// available -- reported media_available: false for the entire duration of every
// call. The browser waits for that flag before it offers WebRTC, so the call
// connected, the far end answered, and the page sat on "ringing" forever with
// not one ICE candidate exchanged.
//
// Measured on hardware: the agent's own snapshot reported media_available true
// while /calls/active reported false throughout a call in phase active.
//
// A capability the software discovers for itself must not require a hand-written
// file to become usable. So the port is resolved here instead: the physical
// device path is in the port string, the module's USB audio interface appears
// under that path in sysfs, and the host ALSA card number can be read off it.
//
// The bindings FILE still wins wherever it names a port. It is the override for
// a board this resolution gets wrong, and an operator who has written one
// should not have it second-guessed.

// QuectelUACPortPrefix marks an audio port that names a module's USB audio
// device by its sysfs path. It matches the prefix the modem provider builds.
const QuectelUACPortPrefix = "quectel-uac:"

// soundClassDirectory is where the kernel lists ALSA cards. A variable so the
// resolution can be tested against a fixture tree rather than the host.
var soundClassDirectory = "/sys/class/sound"

// derivedBindingTTL bounds how long a resolution is reused.
//
// Both hits and misses are cached, and the miss matters more: IsConfigured runs
// on every snapshot, and a line whose card is not present must not walk sysfs
// several times a second. A hit is cached for the same window rather than
// forever because a card number is not stable across re-enumeration -- pull the
// module and put it back and it may come up as a different card. Ten seconds is
// short enough that a re-plug is picked up before anybody dials, and long
// enough that the walk is not on the hot path.
const derivedBindingTTL = 10 * time.Second

type derivedBinding struct {
	binding  Binding
	resolved bool
	at       time.Time
}

type bindingResolver struct {
	mu    sync.Mutex
	cache map[string]derivedBinding
	now   func() time.Time
}

func (r *bindingResolver) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// resolve returns the derived binding for a port, or false when there is none.
func (r *bindingResolver) resolve(audioPort string) (Binding, bool) {
	port := strings.TrimSpace(audioPort)
	if !strings.HasPrefix(port, QuectelUACPortPrefix) {
		return Binding{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if cached, found := r.cache[port]; found &&
		r.clock().Sub(cached.at) < derivedBindingTTL {
		return cached.binding, cached.resolved
	}
	binding, err := DeriveQuectelUACBinding(port)
	entry := derivedBinding{binding: binding, resolved: err == nil, at: r.clock()}
	if r.cache == nil {
		r.cache = make(map[string]derivedBinding, 1)
	}
	// Logged when the OUTCOME changes, not on every resolution. A failure here
	// is the difference between a page that sits on "ringing" forever and one
	// whose operator can find out why in the log, so it must be visible -- but
	// the resolution runs on every snapshot, so logging each attempt would bury
	// it. The previous outcome is remembered across the TTL for exactly this.
	previous, known := r.cache[port]
	if !known || previous.resolved != entry.resolved {
		if entry.resolved {
			slog.Info(
				"resolved the module USB audio device for call media",
				"component", "media",
				"audio_port", port,
				"endpoint", binding.Endpoint,
			)
		} else {
			slog.Warn(
				"no media binding could be derived for a module audio port; calls on "+
					"this line will report media as unavailable and the browser will "+
					"never start the audio exchange",
				"component", "media",
				"audio_port", port,
				"error", err,
			)
		}
	}
	r.cache[port] = entry
	return entry.binding, entry.resolved
}

// DeriveQuectelUACBinding resolves a Quectel UAC audio port to an ALSA binding.
//
// The port carries the module's sysfs physical device path. Every ALSA card
// exposes a "device" symlink pointing at the device that created it, so the
// card belonging to this module is the one whose device resolves to something
// inside the module's own path -- in practice the USB audio interface, a child
// of it. The card number in the directory name becomes the hw: endpoint.
//
// Exported so the agent can report what it resolved, and so a failure can be
// explained rather than appearing as an unbound port.
func DeriveQuectelUACBinding(audioPort string) (Binding, error) {
	port := strings.TrimSpace(audioPort)
	physicalDevice := strings.TrimSpace(strings.TrimPrefix(port, QuectelUACPortPrefix))
	if !strings.HasPrefix(port, QuectelUACPortPrefix) || physicalDevice == "" {
		return Binding{}, fmt.Errorf("audio port %q does not name a module USB audio device", audioPort)
	}
	// Resolved so a path reached through a symlink still matches the cards'
	// resolved device paths, which are always canonical.
	canonical, err := filepath.EvalSymlinks(physicalDevice)
	if err != nil {
		return Binding{}, fmt.Errorf("resolve module device path %q: %w", physicalDevice, err)
	}

	entries, err := os.ReadDir(soundClassDirectory)
	if err != nil {
		return Binding{}, fmt.Errorf("list ALSA cards: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "card") {
			continue
		}
		number := strings.TrimPrefix(name, "card")
		if number == "" || strings.ContainsAny(number, "DdCcPp") {
			// cardN is a card; cardNDnp and the like are its PCM devices.
			continue
		}
		device, err := filepath.EvalSymlinks(filepath.Join(soundClassDirectory, name, "device"))
		if err != nil {
			continue
		}
		if !underPath(device, canonical) {
			continue
		}
		return Binding{
			AudioPort: port,
			Backend:   BackendALSAPCM,
			// Device 0 of the card: the module's USB audio gadget exposes one
			// playback/capture pair, and the ALSA command backend passes this
			// name verbatim to arecord and aplay.
			Endpoint: "hw:" + number + ",0",
		}, nil
	}
	return Binding{}, fmt.Errorf(
		"no ALSA card belongs to module device %q; the module's USB audio interface "+
			"is not present (check that usbcfg has audio enabled and the host bound "+
			"snd-usb-audio)",
		physicalDevice,
	)
}

// underPath reports whether candidate is root or sits beneath it.
//
// A string prefix test alone would accept "/sys/devices/.../usb1/1-100" as
// being under ".../usb1/1-10", which is a different module in a different
// socket -- and binding a call to another modem's sound card is worse than
// failing to bind it at all.
func underPath(candidate, root string) bool {
	candidate = filepath.Clean(candidate)
	root = filepath.Clean(root)
	if candidate == root {
		return true
	}
	return strings.HasPrefix(candidate, root+string(filepath.Separator))
}
