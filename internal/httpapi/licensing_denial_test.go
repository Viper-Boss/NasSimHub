package httpapi

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/license"
)

// A build that cannot verify certificates must say so, not point at a shop.
//
// With MODEMDECK_LICENSE_ISSUER_PUBLIC_KEY unset the store holds no issuer key,
// so no certificate can ever verify and every paid feature is locked. The
// operator then sees a refusal that reads exactly like a normal paywall and
// tells them to send us their machine ID -- which cannot help, because nothing
// we signed back would be accepted. Somebody who has already paid is sent to
// the wrong place, and a broken deployment hides behind a plausible message.
//
// Observed on a real installation: outbound dialling was refused on the
// owner's own gateway with "把本机的授权 ID 发给我们即可开通", and the missing
// licence was not the problem.
func TestARefusalFromABuildWithNoIssuerKeySaysSo(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	api, err := New(&fakeRepository{}, Options{
		disableAuthentication:  true,
		ContactBackupDirectory: filepath.Join(root, "backups", "contacts"),
		// No issuer key, which is what an unconfigured deployment looks like.
	})
	if err != nil {
		t.Fatal(err)
	}
	message := api.licenseDenial(license.Decision{
		Missing: []license.Scope{license.ScopeModule},
	})
	if !strings.Contains(message, "MODEMDECK_LICENSE_ISSUER_PUBLIC_KEY") {
		t.Fatalf(
			"a build that can verify nothing refused with %q; it does not name the "+
				"reason, so the operator is sent to buy something that could not work",
			message,
		)
	}
}

// And a build that CAN verify must still name what to buy, or the paywall is
// just a wall. Guarded here as well as in license_gate_test.go because the fix
// above added a branch that could swallow every refusal.
func TestARefusalFromAConfiguredBuildNamesTheScope(t *testing.T) {
	api, _, _ := licensedAPI(t)
	message := api.licenseDenial(license.Decision{
		Missing: []license.Scope{license.ScopeModule},
	})
	if strings.Contains(message, "MODEMDECK_LICENSE_ISSUER_PUBLIC_KEY") {
		t.Fatalf("a configured build blamed its own configuration: %q", message)
	}
	if !strings.Contains(message, "模组授权") {
		t.Fatalf("a refusal that does not say what is needed: %q", message)
	}
}
