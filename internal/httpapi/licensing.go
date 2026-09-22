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
func (api *API) entitlements(ctx context.Context) license.Entitlements {
	runtime := api.licensing()
	if runtime == nil {
		// No data directory: fixture mode, or storage that is not configured.
		// An empty Entitlements grants exactly the free tier, which is the
		// right answer -- it is what an installation with no certificates has.
		return license.Entitlements{}
	}
	machine, _ := runtime.machineID()
	return runtime.store.Entitlements(machine, api.moduleSerials(ctx), time.Now())
}

// featureDecision is the one call every gate makes.
func (api *API) featureDecision(
	ctx context.Context,
	feature license.Feature,
) license.Decision {
	if !api.licenseEnforced {
		// Test builds only; see Options.disableLicenseEnforcement.
		return license.Decision{Allowed: true, Licensed: true}
	}
	return api.entitlements(ctx).Check(feature)
}

// licensedFeature reports whether a certificate covers this feature.
//
// This is the question the two outbound paths ask, and it is deliberately NOT
// "may I". Calling and texting are free within the anti-fraud guard's daily
// limit, and what a licence buys is that the guard does not apply. A path that
// asked "may I" would have somewhere to render a refusal, and for outbound
// calling there must be no refusal to render: the guard's own notice is the
// only thing an operator over the limit is shown.
func (api *API) licensedFeature(ctx context.Context, feature license.Feature) bool {
	return api.featureDecision(ctx, feature).Licensed
}

// licenseDenial renders a refusal the operator can act on.
//
// It is a method because the right message depends on something the decision
// cannot know: whether this build can verify a certificate at all. If the
// store holds no issuer key then NO certificate can ever verify and every paid
// feature is locked -- and the message below would tell the operator to send
// us their machine ID, which would achieve exactly nothing. That misdirection
// is expensive: it sends somebody who has already paid to the wrong place, and
// it hides a broken deployment behind a plausible paywall. So the two cases
// are named separately.
//
// This branch used to mean "the deployment forgot to set
// MODEMDECK_LICENSE_ISSUER_PUBLIC_KEY", and said so. The key is now compiled
// in (internal/license/issuer_key.go) and checked by a test that fails the
// build if it is unusable, so there is no longer any way for an operator to
// cause this -- or to fix it. Reaching it means the binary itself is wrong:
// built from a tree with the constant emptied, or modified afterwards. So the
// message no longer asks for configuration that does not exist; it says the
// build is not a proper release and points at the one thing that helps.
func (api *API) licenseDenial(decision license.Decision) string {
	if runtime := api.licensing(); runtime == nil || !runtime.store.Configured() {
		return "此功能需要授权，但当前程序内置的授权公钥无效，因此任何证书都无法校验。" +
			"这不是配置问题，也不是授权问题——请重新安装官方发布的版本。"
	}
	return licenseDenialText(decision)
}

// licenseDenialText names what would have to be licensed rather than saying
// "not permitted", because a paywall that does not say what to buy is just a
// wall.
func licenseDenialText(decision license.Decision) string {
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
