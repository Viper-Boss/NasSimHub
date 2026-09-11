package httpapi

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Every agent-backed service the API declares must actually be handed one.
//
// This test exists because the DHT was not. httpapi.Options had a DHT field,
// the DHT settings endpoint read api.dhtService, the whole feature was built
// and tested against the fixture -- and main.go never passed the agent in. So
// api.dhtService was nil in every production build and every read answered
// "DHT runtime unavailable: the agent does not offer this capability".
//
// The reason it survived that long is the shape of the failure. A nil optional
// service is a LEGITIMATE state: it is exactly what an older agent looks like,
// and the code handles it correctly and politely. There is no crash, no error
// log, no failing test -- just a feature that is permanently unavailable and
// an explanation that sounds plausible. Nothing distinguishes "not wired" from
// "not supported" at runtime, which is precisely why it has to be caught here
// instead.
//
// So this walks the Options struct and asserts that every field whose type is
// an agent-backed service interface is assigned somewhere in main.go. It is a
// source check rather than a behavioural one because the bug lives in the
// wiring, which is the one thing a test using its own Options literal cannot
// see.

// agentBackedServices are the Options fields that must reach the agent.
//
// Listed explicitly rather than inferred, because "is this interface backed by
// the agent" is a judgement: CallPolicies and LineServices are satisfied by
// the communications service, RTCConfiguration by a TURN provider. Guessing
// would make this test either miss the next one or fail on things that are
// correctly wired elsewhere.
var agentBackedServices = []string{
	"WireGuard",
	"DHT",
	"CallServices",
}

// configuredFromEnvironment are Options fields that are not services but have
// the same failure mode: silently empty, with a plausible-looking consequence.
// An unset issuer key locks every paid feature and reports it as "not
// licensed", which is indistinguishable from a customer who has not paid.
var configuredFromEnvironment = []string{
	"IssuerPublicKey",
}

func TestConfiguredOptionsAreReadFromTheEnvironmentInMain(t *testing.T) {
	main, err := os.ReadFile(filepath.Join("..", "..", "cmd", "modemdeck", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(main)
	options := reflect.TypeOf(Options{})
	for _, name := range configuredFromEnvironment {
		if _, found := options.FieldByName(name); !found {
			t.Fatalf("Options has no field %q; this list is stale", name)
		}
		if !regexp.MustCompile(`(?m)^\s*` + name + `:\s*\S`).MatchString(source) {
			t.Fatalf(
				"Options.%s is never assigned in cmd/modemdeck/main.go, so it is empty in "+
					"production and the consequence looks like a legitimate state",
				name,
			)
		}
	}
}

func TestEveryAgentBackedServiceIsWiredInMain(t *testing.T) {
	main, err := os.ReadFile(filepath.Join("..", "..", "cmd", "modemdeck", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(main)

	options := reflect.TypeOf(Options{})
	for _, name := range agentBackedServices {
		field, found := options.FieldByName(name)
		if !found {
			t.Fatalf(
				"Options has no field %q; this list is stale and the test is guarding nothing",
				name,
			)
		}
		if field.Type.Kind() != reflect.Interface {
			t.Fatalf("Options.%s is %s, not an interface; the list needs revisiting", name, field.Type.Kind())
		}
		// The assignment has to be inside the httpapi.Options literal, not
		// merely somewhere in the file, so the search is anchored to it.
		assignment := regexp.MustCompile(`(?m)^\s*` + name + `:\s*\S`)
		if !assignment.MatchString(source) {
			t.Fatalf(
				"Options.%s is never assigned in cmd/modemdeck/main.go, so api.%s is nil in production "+
					"and the feature reports itself unavailable with no way to tell that apart from "+
					"an older agent",
				name, strings.ToLower(name[:1])+name[1:],
			)
		}
	}
}

// A nil service must still answer, and must say which kind of nothing it is.
//
// The panel needs to render and explain itself; a 500 or a dropped connection
// gives it nothing to explain with. And "the agent cannot do this" has to be
// distinguishable from "the network refused", because the first is a build
// problem and the second is something the operator can retry.
func TestAMissingCallServiceExplainsItselfRatherThanFailing(t *testing.T) {
	api, err := New(&fakeRepository{}, Options{
		disableAuthentication:     true,
		disableLicenseEnforcement: true,
		ContactBackupDirectory:    filepath.Join(t.TempDir(), "backups", "contacts"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if api.callServices != nil {
		t.Fatal("a service was created from nothing")
	}
}
