package httpapi

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/internal/license"
)

// The bridge between the licence package and the request paths that need it.
//
// Two things live here and both are about cost. Resolving the machine identity
// reads a dozen sysfs files, so it is done once and kept. Building the module
// serial list walks the current device snapshot, which is cheap but changes
// when somebody plugs a modem in -- so it is rebuilt per check and the
// entitlement cache keys on it.
//
// The important rule, stated once for every caller: nothing outside this file
// asks "is there a certificate". It asks Check(feature) and acts on the answer.
// A feature gate spread across call sites cannot be changed without hunting for
// all of them, which is how something ends up half-gated.

// licenseRuntime is the resolved licence state for this process.
type licenseRuntime struct {
	store   *license.Store
	usage   *license.UsageCounter
	once    sync.Once
	machine string
	// machineError records why the identity could not be resolved. A board with
	// no burned-in identifier gets no machine licence, and the settings page has
	// to be able to say that rather than showing an unexplained lock.
	machineError string
}

// licensing resolves the runtime on first use.
//
// It is built lazily rather than at construction because the data directory is
// not always available (fixture mode has none) and because reading the machine
// fingerprint at start-up would put a dozen sysfs reads in the boot path of a
// gateway whose owner may never buy anything.
func (api *API) licensing() *licenseRuntime {
	api.licenseOnce.Do(func() {
		directory, ok := api.homeSIMDataDirectory()
		if !ok {
			return
		}
		api.licenseRuntimeInstance = &licenseRuntime{
			store: license.NewStore(directory, api.issuerPublicKey),
			usage: license.NewUsageCounter(directory),
		}
	})
	return api.licenseRuntimeInstance
}

// machineID resolves and remembers this gateway's identity.
func (runtime *licenseRuntime) machineID() (string, string) {
	runtime.once.Do(func() {
		print, err := license.CollectFingerprint("")
		if err != nil {
			runtime.machineError = err.Error()
			return
		}
		runtime.machine = print.MachineID
		// Recorded the first time it is resolved and never rewritten, so there
		// is a record of what the machine looked like when it was activated.
		// That record is the only way to tell a repaired board from a
		// different one later.
		_ = runtime.store.SaveFingerprint(print)
	})
	return runtime.machine, runtime.machineError
}

// moduleSerials lists the modules currently attached, by IMEI.
//
// IMEI rather than the vendor serial the design document named, and the
// difference is worth stating: the IMEI is burned into the modem, unique per
// unit, already collected on every snapshot, and already shown on the module
// card -- so a customer can read their own module's identity off the screen
// without a tool. The vendor SN is equally unique but this software never sees
// it, which would mean adding an AT query to support a value nobody can look
// up. Certificates therefore bind ModuleSN to an IMEI.
//
// A module certificate counts only while its module is present; that is what
// makes a licence follow the module rather than the box. Sorted so the
// entitlement cache key is stable across calls.
func (api *API) moduleSerials(ctx context.Context) []string {
	serials := []string{}
	if api.repository == nil {
		return serials
	}
	devices, err := api.repository.Devices(ctx)
	if err != nil {
		// An unreadable device list is reported as "no modules attached",
		// which locks module features rather than unlocking them. Failing
		// closed is right for a paywall; failing open would hand out every
		// module feature the first time the database hiccupped.
		return serials
	}
	seen := map[string]bool{}
	for _, device := range devices {
		serial := strings.TrimSpace(device.IMEI)
		if serial == "" || seen[serial] {
			continue
		}
		seen[serial] = true
		serials = append(serials, serial)
	}
	sort.Strings(serials)
	return serials
}

// entitlements evaluates the licence state for the current hardware.
func (api *API) entitlements(ctx context.Context) (license.Entitlements, *license.UsageCounter) {
	runtime := api.licensing()
	if runtime == nil {
		// No data directory: fixture mode, or storage that is not configured.
		// An empty Entitlements grants exactly the free tier, which is the
		// right answer -- it is what an installation with no certificates has.
		return license.Entitlements{}, nil
	}
	machine, _ := runtime.machineID()
	return runtime.store.Entitlements(machine, api.moduleSerials(ctx), time.Now()), runtime.usage
}

// featureDecision is the one call every gate makes.
func (api *API) featureDecision(
	ctx context.Context,
	feature license.Feature,
) (license.Decision, *license.UsageCounter) {
	if !api.licenseEnforced {
		// Test builds only; see Options.disableLicenseEnforcement. Granting
		// unlimited rather than the free tier, so a test exercising a paid path
		// is not silently metered.
		return license.Decision{Allowed: true, Licensed: true}, nil
	}
	entitlements, usage := api.entitlements(ctx)
	return entitlements.Check(feature), usage
}

// licenseDenial renders a refusal the operator can act on.
//
// The message names what would have to be licensed rather than saying "not
// permitted", because a paywall that does not say what to buy is just a wall.
func licenseDenial(decision license.Decision) string {
	scopes := make([]string, 0, len(decision.Missing))
	for _, scope := range decision.Missing {
		switch scope {
		case license.ScopeMachine:
			scopes = append(scopes, "本机授权")
		case license.ScopeModule:
			scopes = append(scopes, "模组授权")
		}
	}
	if len(scopes) == 0 {
		return "此功能需要授权。"
	}
	return "此功能需要" + strings.Join(scopes, " 和 ") + "。把本机的授权 ID 发给我们即可开通。"
}
