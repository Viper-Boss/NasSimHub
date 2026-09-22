package httpapi

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/license"
)

// A build that cannot verify certificates must say so, not point at a shop.
//
// A store with no issuer key can verify no certificate, so every paid feature
// is locked. The operator then sees a refusal that reads exactly like a normal
// paywall and tells them to send us their machine ID -- which cannot help,
// because nothing we signed back would be accepted. Somebody who has already
// paid is sent to the wrong place, and a broken deployment hides behind a
// plausible message.
//
// Observed on a real installation: outbound dialling was refused on the
// owner's own gateway with "把本机的授权 ID 发给我们即可开通", and the missing
// licence was not the problem.
//
// The issuer key is compiled in now, so a release build cannot reach this
// state and an operator can neither cause it nor fix it. The branch stays
// because a build made from a tree with the constant emptied still can, and
// what it says then has to be "this binary is wrong", not "configure it".
func TestARefusalFromABuildWithNoIssuerKeySaysSo(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	api, err := New(&fakeRepository{}, Options{
		disableAuthentication:  true,
		ContactBackupDirectory: filepath.Join(root, "backups", "contacts"),
		// No issuer key, which is what a build with the compiled-in constant
		// emptied looks like.
	})
	if err != nil {
		t.Fatal(err)
	}
	message := api.licenseDenial(license.Decision{
		Missing: []license.Scope{license.ScopeModule},
	})
	if !strings.Contains(message, "内置的授权公钥") {
		t.Fatalf(
			"a build that can verify nothing refused with %q; it does not name the "+
				"reason, so the operator is sent to buy something that could not work",
			message,
		)
	}
	// And it must not send them to edit configuration that no longer exists.
	if strings.Contains(message, "MODEMDECK_LICENSE_ISSUER_PUBLIC_KEY") {
		t.Fatalf("the refusal still points at a removed environment variable: %q", message)
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
	if strings.Contains(message, "内置的授权公钥") {
		t.Fatalf("a configured build blamed its own binary: %q", message)
	}
	if !strings.Contains(message, "模组授权") {
		t.Fatalf("a refusal that does not say what is needed: %q", message)
	}
}
