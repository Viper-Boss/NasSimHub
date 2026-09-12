package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A module USB audio port must resolve to its own ALSA card with no
// configuration file at all.
//
// This is the test for the fault that made the browser sit on "ringing"
// forever. Bindings came only from -media-bindings, keyed by the line's sysfs
// path; the agent default for that file is empty, so IsConfigured answered
// false for every port, the server withheld media_available for the whole of
// every call, and the page never offered WebRTC. Measured on hardware: the
// agent reported media_available true while /calls/active reported false
// throughout a call in phase active, with zero ICE candidates exchanged.

// sysfsFixture builds a sound class tree and returns the module's device path.
//
// Laid out the way the kernel does: the card's "device" symlink points at the
// USB INTERFACE, which is a child of the module's device, not the device
// itself. A resolution that only accepted an exact match would find nothing on
// real hardware.
func sysfsFixture(t *testing.T) (moduleDevice string, cards string) {
	t.Helper()
	root := t.TempDir()
	moduleDevice = filepath.Join(root, "devices", "pci0000:00", "usb1", "1-10")
	audioInterface := filepath.Join(moduleDevice, "1-10:1.2")
	if err := os.MkdirAll(audioInterface, 0o755); err != nil {
		t.Fatal(err)
	}
	cards = filepath.Join(root, "class", "sound")
	if err := os.MkdirAll(cards, 0o755); err != nil {
		t.Fatal(err)
	}
	return moduleDevice, cards
}

func linkCard(t *testing.T, cards, name, device string) {
	t.Helper()
	cardDirectory := filepath.Join(cards, name)
	if err := os.MkdirAll(cardDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(device, filepath.Join(cardDirectory, "device")); err != nil {
		t.Fatal(err)
	}
}

func useFixture(t *testing.T, cards string) {
	t.Helper()
	previous := soundClassDirectory
	soundClassDirectory = cards
	t.Cleanup(func() { soundClassDirectory = previous })
}

func TestAModuleAudioPortResolvesToItsOwnALSACard(t *testing.T) {
	moduleDevice, cards := sysfsFixture(t)
	useFixture(t, cards)
	linkCard(t, cards, "card2", filepath.Join(moduleDevice, "1-10:1.2"))

	binding, err := DeriveQuectelUACBinding(QuectelUACPortPrefix + moduleDevice)
	if err != nil {
		t.Fatalf(
			"the module's audio port could not be resolved: %v; without a binding the "+
				"server withholds media_available and the page never offers WebRTC",
			err,
		)
	}
	if binding.Backend != BackendALSAPCM {
		t.Fatalf("backend = %q", binding.Backend)
	}
	if binding.Endpoint != "hw:2,0" {
		t.Fatalf("endpoint = %q; want the module's own card", binding.Endpoint)
	}
	if binding.AudioPort != QuectelUACPortPrefix+moduleDevice {
		t.Fatalf("audio port = %q", binding.AudioPort)
	}
}

// Another modem's card must never be chosen. Binding a call to the wrong
// module's sound card is worse than failing to bind it: the call would carry
// somebody else's audio, or silence, and look configured either way.
func TestAnotherModulesCardIsNotChosen(t *testing.T) {
	moduleDevice, cards := sysfsFixture(t)
	useFixture(t, cards)
	// A sibling in a different socket whose path shares the prefix, which is
	// exactly what a plain string comparison gets wrong: ".../1-100" starts
	// with ".../1-10".
	other := filepath.Join(filepath.Dir(moduleDevice), "1-100", "1-100:1.2")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	linkCard(t, cards, "card0", other)

	if binding, err := DeriveQuectelUACBinding(QuectelUACPortPrefix + moduleDevice); err == nil {
		t.Fatalf(
			"resolved to %q, which belongs to the module in another socket",
			binding.Endpoint,
		)
	}
}

// A PCM device entry is not a card. /sys/class/sound holds cardN alongside
// cardNdM entries, and picking one of those produces an endpoint no ALSA tool
// can open.
func TestPCMDeviceEntriesAreNotMistakenForCards(t *testing.T) {
	moduleDevice, cards := sysfsFixture(t)
	useFixture(t, cards)
	audioInterface := filepath.Join(moduleDevice, "1-10:1.2")
	linkCard(t, cards, "card1d0p", audioInterface)
	linkCard(t, cards, "card1", audioInterface)

	binding, err := DeriveQuectelUACBinding(QuectelUACPortPrefix + moduleDevice)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Endpoint != "hw:1,0" {
		t.Fatalf("endpoint = %q; a PCM device entry was treated as a card", binding.Endpoint)
	}
}

// With no card present the failure has to explain itself, because "no audio"
// and "the module's audio interface is not enumerated" need different actions.
func TestAMissingCardIsExplained(t *testing.T) {
	moduleDevice, cards := sysfsFixture(t)
	useFixture(t, cards)

	_, err := DeriveQuectelUACBinding(QuectelUACPortPrefix + moduleDevice)
	if err == nil {
		t.Fatal("a module with no ALSA card resolved anyway")
	}
	if !strings.Contains(err.Error(), "usbcfg") {
		t.Fatalf("the failure does not say what to check: %v", err)
	}
}

// The bindings file wins. It is the override for a board this resolution gets
// wrong, and an operator who wrote one must not have it second-guessed.
func TestAnExplicitBindingOverridesTheDerivedOne(t *testing.T) {
	moduleDevice, cards := sysfsFixture(t)
	useFixture(t, cards)
	linkCard(t, cards, "card2", filepath.Join(moduleDevice, "1-10:1.2"))

	port := QuectelUACPortPrefix + moduleDevice
	manager, err := NewManager(
		CallSourceFunc(nil),
		[]Binding{{AudioPort: port, Backend: BackendCharPCM, Endpoint: "/dev/ttyGS0"}},
		map[BackendKind]Backend{
			BackendCharPCM: NewCharPCMBackend(nil),
			BackendALSAPCM: NewALSAPCMBackend(nil),
		},
		Options{},
	)
	if err != nil {
		t.Fatal(err)
	}
	binding, found := manager.binding(port)
	if !found {
		t.Fatal("the explicit binding was lost")
	}
	if binding.Backend != BackendCharPCM || binding.Endpoint != "/dev/ttyGS0" {
		t.Fatalf("the derived binding overrode the configured one: %+v", binding)
	}
}

// And a port the file does not name is derived, which is the whole point: no
// bindings file is the normal case.
func TestAPortWithNoFileEntryIsDerived(t *testing.T) {
	moduleDevice, cards := sysfsFixture(t)
	useFixture(t, cards)
	linkCard(t, cards, "card2", filepath.Join(moduleDevice, "1-10:1.2"))

	manager, err := NewManager(
		CallSourceFunc(nil),
		nil,
		map[BackendKind]Backend{BackendALSAPCM: NewALSAPCMBackend(nil)},
		Options{},
	)
	if err != nil {
		t.Fatal(err)
	}
	port := QuectelUACPortPrefix + moduleDevice
	if !manager.IsConfigured(port) {
		t.Fatal(
			"a module audio port with no bindings file entry reported itself " +
				"unconfigured; the server then withholds media_available for the " +
				"whole call and the page never leaves ringing",
		)
	}
}

// A derived binding whose backend was never built must NOT report configured.
// Saying otherwise moves the same failure one step later, to an open that
// cannot succeed.
func TestADerivedBindingWithoutItsBackendIsNotConfigured(t *testing.T) {
	moduleDevice, cards := sysfsFixture(t)
	useFixture(t, cards)
	linkCard(t, cards, "card2", filepath.Join(moduleDevice, "1-10:1.2"))

	manager, err := NewManager(
		CallSourceFunc(nil),
		nil,
		map[BackendKind]Backend{BackendCharPCM: NewCharPCMBackend(nil)},
		Options{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if manager.IsConfigured(QuectelUACPortPrefix + moduleDevice) {
		t.Fatal("a port reported configured with no ALSA backend to open it")
	}
}

// Misses are cached, or IsConfigured walks sysfs several times a second for a
// line whose module is unplugged.
func TestAnUnresolvedPortIsNotWalkedRepeatedly(t *testing.T) {
	moduleDevice, cards := sysfsFixture(t)
	useFixture(t, cards)

	now := time.Now()
	resolver := &bindingResolver{now: func() time.Time { return now }}
	port := QuectelUACPortPrefix + moduleDevice
	if _, found := resolver.resolve(port); found {
		t.Fatal("a module with no card resolved")
	}
	// The card appears, but within the TTL the cached miss still stands. That
	// is the trade the cache makes, and it is bounded.
	linkCard(t, cards, "card2", filepath.Join(moduleDevice, "1-10:1.2"))
	if _, found := resolver.resolve(port); found {
		t.Fatal("the miss was not cached; sysfs is walked on every snapshot")
	}
	// And once the window passes it is retried, so a module plugged in later
	// starts working without restarting the agent.
	now = now.Add(derivedBindingTTL + time.Second)
	if _, found := resolver.resolve(port); !found {
		t.Fatal("the cached miss never expired, so a module plugged in later never works")
	}
}

// A port that is not a module audio port is never derived. char-pcm lines are
// bound by the file and must stay that way.
func TestOnlyModuleAudioPortsAreDerived(t *testing.T) {
	resolver := &bindingResolver{}
	if _, found := resolver.resolve("/dev/ttyGS0"); found {
		t.Fatal("a character device port was derived as an ALSA card")
	}
}
