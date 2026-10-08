package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/communication"
	"github.com/human-agent65535/modemdeck/internal/mobilepairing"
	"github.com/human-agent65535/modemdeck/internal/store"
)

// Route audiences.
//
// Every static route the router knows is listed here with who may reach it.
// TestAdversarialEveryStaticRouteHasADeclaredAudience parses api.go and fails
// when a route appears that is not in this table, so a new endpoint cannot be
// added without somebody deciding -- and a test then checking -- whether a
// member may call it.
type advAudience int

const (
	// advPublic is answered before the authorisation gate.
	advPublic advAudience = iota
	// advMember needs a session; the router lets any role through and the
	// handler scopes by line or by owner.
	advMember
	// advAdmin is refused for a member whatever the method.
	advAdmin
	// advAdminWrite lets a member read and refuses every other method.
	advAdminWrite
	// advAdminPost is refused for a member on POST; the handler answers 405
	// to every other method before it looks at the role.
	advAdminPost
	// advAdminGet is refused for a member on GET (GET-only handler).
	advAdminGet
)

var advStaticRoutes = map[string]advAudience{
	"/api/v1/health/live":                advPublic,
	"/api/v1/health":                     advPublic,
	"/api/v1/health/ready":               advPublic,
	"/api/v1/version":                    advPublic,
	mobilepairing.CloudflareProbePath:    advPublic,
	"/api/v1/session":                    advPublic,
	"/api/v1/setup":                      advPublic,
	"/api/v1/provision":                  advPublic,
	"/api/v1/provision/reset":            advPublic,
	"/api/v1/mobile/enrollment/exchange": advPublic,

	"/api/v1/mobile/session":              advMember,
	"/api/v1/bootstrap":                   advMember,
	"/api/v1/account/password":            advMember,
	"/api/v1/account/contact":             advMember,
	"/api/v1/account/preferences":         advMember,
	"/api/v1/account/sessions":            advMember,
	"/api/v1/account/sessions/others":     advMember,
	"/api/v1/mobile/pairing":              advMember,
	"/api/v1/mobile/push":                 advMember,
	"/api/v1/mobile/push/test-call":       advMember,
	"/api/v1/mobile/enrollment":           advMember,
	"/api/v1/contacts":                    advMember,
	"/api/v1/contacts/batch":              advMember,
	"/api/v1/contacts/backup":             advMember,
	"/api/v1/homesim/phone-geo":           advMember,
	"/api/v1/messages/threads":            advMember,
	"/api/v1/messages":                    advMember,
	"/api/v1/messages/read":               advMember,
	"/api/v1/messages/threads/state":      advMember,
	"/api/v1/messages/events":             advMember,
	"/api/v1/runtime/events":              advMember,
	"/api/v1/calls":                       advMember,
	"/api/v1/calls/missed/read":           advMember,
	"/api/v1/calls/batch":                 advMember,
	"/api/v1/calls/active":                advMember,
	"/api/v1/security/posture":            advMember,
	"/api/v1/stats/communication":         advMember, // ?node_id= is admin only, tested separately
	"/api/v1/recordings":                  advMember,
	"/api/v1/recordings/batch":            advMember,
	"/api/v1/network":                     advMember,
	"/api/v1/proxies":                     advMember,
	"/api/v1/vowifi-diagnostics":          advMember,
	"/api/v1/system-metrics":              advMember,
	"/api/v1/settings/telegram":           advMember,
	"/api/v1/settings/lines":              advMember,
	"/api/v1/settings/system":             advMember,
	"/api/v1/settings/recording":          advMember,
	"/api/v1/nkn/chat/messages":           advMember, // finding A-11: shared mailbox
	"/api/v1/nkn/chat/clear":              advMember,
	"/api/v1/nkn/chat/send":               advMember,
	"/api/v1/nkn/chat/peers":              advMember,
	"/api/v1/devices":                     advAdminWrite,
	"/api/v1/users":                       advAdmin,
	"/api/v1/calls/guard":                 advAdminPost,
	"/api/v1/security/module":             advAdminGet,
	"/api/v1/about":                       advAdmin,
	"/api/v1/updates/check":               advAdmin,
	"/api/v1/updates/apply":               advAdmin,
	"/api/v1/updates/status":              advAdmin,
	"/api/v1/updates/download":            advAdmin,
	"/api/v1/updates/preferences":         advAdmin,
	"/api/v1/updates/sharing":             advAdmin,
	"/api/v1/external-access/status":      advAdmin,
	"/api/v1/external-access/refresh":     advAdmin,
	"/api/v1/external-access/origin-tls":  advAdmin,
	"/api/v1/homesim/f50/status":          advAdmin,
	"/api/v1/homesim/f50/sms":             advAdmin,
	"/api/v1/homesim/f50/sms/send":        advAdmin,
	"/api/v1/homesim/f50/sms/read":        advAdmin,
	"/api/v1/diagnostics":                 advAdmin,
	"/api/v1/diagnostics/logs":            advAdmin,
	"/api/v1/diagnostics/logs/stream":     advAdmin,
	"/api/v1/diagnostics/logs/download":   advAdmin,
	"/api/v1/settings/calls":              advAdmin,
	"/api/v1/settings/tls":                advAdmin,
	"/api/v1/settings/tls/ca":             advAdmin,
	"/api/v1/settings/nkn-node":           advAdmin,
	"/api/v1/settings/nkn-node/wallet":    advAdmin,
	"/api/v1/settings/nkn-node/logs":      advAdmin,
	"/api/v1/settings/nkn-network":        advAdmin,
	"/api/v1/settings/nkn-network/peers":  advAdmin,
	"/api/v1/settings/nkn-network/nodes":  advAdmin,
	"/api/v1/settings/homesim":            advAdmin,
	"/api/v1/settings/homesim/nkn":        advAdmin,
	"/api/v1/settings/homesim/wireguard":  advAdmin,
	"/api/v1/settings/homesim/dht":        advAdmin,
	"/api/v1/settings/homesim/quotas":     advAdmin,
	"/api/v1/settings/homesim/bark":       advAdmin,
	"/api/v1/settings/homesim/bark/test":  advAdmin,
	"/api/v1/settings/homesim/f50":        advAdmin,
	"/api/v1/settings/homesim/forwarders": advAdmin,

	"/api/v1/settings/homesim/forwarders/test":  advAdmin,
	"/api/v1/settings/homesim/network-switches": advAdmin,
	"/api/v1/settings/homesim/call-filter":      advAdmin,
	"/api/v1/settings/nkn-node/wallet/backup":   advAdmin,
	"/api/v1/nodes":                     advAdmin,
	"/api/v1/nodes/discover":            advAdmin,
	"/api/v1/nodes/pair":                advAdmin,
	"/api/v1/nodes/unpair":              advAdmin,
	"/api/v1/nodes/admin":               advAdmin,
	"/api/v1/nodes/refresh":             advAdmin,
	"/api/v1/nodes/pq-pin/reset":        advAdmin,
	"/api/v1/nodes/updates":             advAdmin,
	"/api/v1/nodes/updates/manifest":    advAdmin,
	"/api/v1/nodes/updates/artifact":    advAdmin,
	"/api/v1/nodes/updates/remove":      advAdmin,
	"/api/v1/nodes/updates/push":        advAdmin,
	"/api/v1/nodes/updates/apply":       advAdmin,
	"/api/v1/nodes/updates/confirm":     advAdmin,
	"/api/v1/nodes/updates/rollback":    advAdmin,
	"/api/v1/security/trust":            advAdmin,
	"/api/v1/security/trust/restrict":   advAdmin,
	"/api/v1/security/trust/quarantine": advAdmin,
	"/api/v1/security/trust/restore":    advAdmin,
	"/api/v1/security/journal":          advAdmin,
	"/api/v1/security/journal/verify":   advAdmin,
	"/api/v1/security/journal/reseal":   advAdmin,
}

// Resource routes the router's default branch resolves, with a placeholder id.
// They carry no secret: every id is one a member could type.
var advAdminResourceRoutes = []string{
	"/api/v1/users/adv-any-user",
	"/api/v1/users/adv-any-user/password",
	"/api/v1/users/adv-any-user/ios-pairing",
	"/api/v1/devices/000000000000000",
	"/api/v1/diagnostics/devices/" + advLineAlpha + "/configuration",
	"/api/v1/nodes/adv-any-node",
	"/api/v1/security/trust/adv-any",
	"/api/v1/security/journal/adv-any",
	"/api/v1/updates/adv-any",
	"/api/v1/settings/adv-any",
	"/api/v1/homesim/f50/adv-any",
}

var advAllMethods = []string{
	http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
}

var advWriteMethods = []string{
	http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
}

// advRouterLiterals extracts the paths ServeHTTP routes by literal: `case
// "/api/v1/..."` arms and `request.URL.Path == "/api/v1/..."` comparisons.
func advRouterLiterals(t *testing.T) []string {
	t.Helper()
	source, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatalf("read api.go: %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func (api *API) ServeHTTP(")
	end := strings.Index(text, "func (api *API) getOnly(")
	if start < 0 || end < start {
		t.Fatal("ServeHTTP was not found in api.go; the route table check cannot run")
	}
	router := text[start:end]
	literal := regexp.MustCompile(`"(/api/v1/[^"]*)"`)
	seen := map[string]bool{}
	for _, line := range strings.Split(router, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "case ") && !strings.Contains(trimmed, "request.URL.Path ==") {
			continue
		}
		for _, match := range literal.FindAllStringSubmatch(trimmed, -1) {
			seen[match[1]] = true
		}
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if len(paths) < 100 {
		t.Fatalf("only %d routes were parsed from api.go; the parser has drifted from the router", len(paths))
	}
	return paths
}

func TestAdversarialEveryStaticRouteHasADeclaredAudience(t *testing.T) {
	routed := map[string]bool{}
	for _, path := range advRouterLiterals(t) {
		routed[path] = true
		if _, declared := advStaticRoutes[path]; !declared {
			t.Errorf("%s is routed in api.go but has no audience in advStaticRoutes: "+
				"decide whether a member may call it and add it to the table", path)
		}
	}
	for path := range advStaticRoutes {
		if path == mobilepairing.CloudflareProbePath {
			continue // routed through the constant, not a literal
		}
		if !routed[path] {
			t.Errorf("%s is in advStaticRoutes but no longer routed in api.go", path)
		}
	}
}

func TestAdversarialMemberIsRefusedOnEveryAdministratorRoute(t *testing.T) {
	harness := newAdvHarness(t, func(options *Options) {
		options.Communications = &advCommunications{}
	})
	member := harness.aliceClient()
	// A member with no lines at all is the least-privileged account there is.
	admin := harness.admin()
	response := harness.updateMember(admin, harness.bob, func(input *updateMemberRequest) {
		input.LineIDs = []string{}
	})
	advRequireStatus(t, response, http.StatusOK, "")
	lineless := harness.bobClient()

	check := func(t *testing.T, path string, methods []string) {
		t.Helper()
		for _, client := range []*advClient{member, lineless} {
			for _, method := range methods {
				body := ""
				if method != http.MethodGet && method != http.MethodDelete {
					body = "{}"
				}
				response := client.do(advRequest{method: method, path: path, body: body})
				if response.Code != http.StatusForbidden || advErrorCode(response) != "admin_required" {
					t.Errorf("%s %s as a member: status = %d code = %q, want 403 admin_required; body = %.200s",
						method, path, response.Code, advErrorCode(response), response.Body)
				}
			}
		}
	}
	for path, audience := range advStaticRoutes {
		switch audience {
		case advAdmin:
			check(t, path, advAllMethods)
		case advAdminWrite:
			check(t, path, advWriteMethods)
		case advAdminPost:
			check(t, path, []string{http.MethodPost})
		case advAdminGet:
			check(t, path, []string{http.MethodGet})
		}
	}
	for _, path := range advAdminResourceRoutes {
		check(t, path, advAllMethods)
	}
	// The one administrator-only query on a member route.
	check(t, "/api/v1/stats/communication?node_id=adv-any-node", []string{http.MethodGet})

	// And the positive control: the same requests are not refused for the
	// administrator, so the table above is testing the role and nothing else.
	for _, path := range []string{"/api/v1/users", "/api/v1/diagnostics/logs", "/api/v1/settings/homesim/f50"} {
		response := admin.get(path)
		if response.Code != http.StatusOK {
			t.Errorf("GET %s as the administrator: %d %s", path, response.Code, response.Body)
		}
	}
}

func TestAdversarialMemberRoutesAreNotMistakenForAdministratorRoutes(t *testing.T) {
	harness := newAdvHarness(t)
	member := harness.aliceClient()
	for path, audience := range advStaticRoutes {
		if audience != advMember && audience != advAdminWrite {
			continue
		}
		if strings.HasSuffix(path, "/events") {
			continue // streams; covered by the event stream tests
		}
		if path == "/api/v1/mobile/push" {
			// Open to every role but only to a paired phone: a browser
			// session is answered 401 by the handler itself.
			continue
		}
		response := member.get(path)
		if response.Code == http.StatusUnauthorized ||
			(response.Code == http.StatusForbidden && advErrorCode(response) == "admin_required") {
			t.Errorf("GET %s as a member: %d %s; advStaticRoutes declares it member-reachable",
				path, response.Code, advErrorCode(response))
		}
	}
}

func TestAdversarialEveryProtectedRouteNeedsASession(t *testing.T) {
	harness := newAdvHarness(t)
	anonymous := harness.anonymous()
	paths := make([]string, 0, len(advStaticRoutes)+len(advAdminResourceRoutes))
	for path, audience := range advStaticRoutes {
		if audience != advPublic {
			paths = append(paths, path)
		}
	}
	paths = append(paths, advAdminResourceRoutes...)
	paths = append(paths,
		"/api/v1/contacts/adv-any", "/api/v1/calls/adv-any", "/api/v1/calls/adv-any/hangup",
		"/api/v1/account/sessions/adv-any", "/api/v1/contacts/backup/contacts-20260101T000000Z-adv1.json",
		"/api/v1/devices/"+advLineAlpha+"/configuration", "/api/v1/lines/"+advLineAlpha+"/label",
		"/api/v1/does-not-exist",
	)
	for _, path := range paths {
		for _, method := range advAllMethods {
			response := anonymous.do(advRequest{method: method, path: path, body: "{}"})
			if response.Code != http.StatusUnauthorized || advErrorCode(response) != "authentication_required" {
				t.Errorf("%s %s without a session: status = %d code = %q, want 401",
					method, path, response.Code, advErrorCode(response))
			}
		}
	}
}

// advCommunications is a CommunicationService that only counts. It is
// goroutine-safe because the duplicate-submission tests call it concurrently.
type advCommunications struct {
	mu          sync.Mutex
	sends       []communication.SendMessageInput
	starts      []communication.StartCallInput
	actions     []communication.CallActionInput
	startGate   chan struct{}
	startedOnce chan struct{}
	once        sync.Once
}

func (service *advCommunications) Status(context.Context) (communication.Status, error) {
	return communication.Status{}, nil
}

func (service *advCommunications) SendMessage(
	_ context.Context,
	input communication.SendMessageInput,
) (store.Message, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.sends = append(service.sends, input)
	return store.Message{LineID: input.LineID}, nil
}

func (service *advCommunications) ReplayStartCall(
	context.Context,
	communication.StartCallInput,
) (store.Call, bool, error) {
	return store.Call{}, false, nil
}

func (service *advCommunications) StartCall(
	_ context.Context,
	input communication.StartCallInput,
) (store.Call, error) {
	service.mu.Lock()
	service.starts = append(service.starts, input)
	gate := service.startGate
	started := service.startedOnce
	service.mu.Unlock()
	if started != nil {
		service.once.Do(func() { close(started) })
	}
	if gate != nil {
		<-gate
	}
	return store.Call{ID: "adv-call-started", LineID: input.LineID, Phase: "dialing"}, nil
}

func (service *advCommunications) ReplayCallAction(
	context.Context,
	communication.CallActionInput,
) (store.Call, bool, error) {
	return store.Call{}, false, nil
}

func (service *advCommunications) CallAction(
	_ context.Context,
	input communication.CallActionInput,
) (store.Call, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.actions = append(service.actions, input)
	return store.Call{}, nil
}

func (service *advCommunications) ActiveCalls(context.Context) ([]store.Call, error) {
	return nil, nil
}

func (service *advCommunications) EndCall(context.Context, string) error { return nil }

func (service *advCommunications) effects() (sends, starts, actions int) {
	service.mu.Lock()
	defer service.mu.Unlock()
	return len(service.sends), len(service.starts), len(service.actions)
}

// advCrossUserHarness is two members with one line each, some history on both
// lines, one contact each, and a live communication service that records any
// command that gets through.
func advCrossUserHarness(t *testing.T) (*advHarness, *advCommunications, *advClient, *advClient, string, string) {
	t.Helper()
	communications := &advCommunications{}
	harness := newAdvHarness(t, func(options *Options) {
		options.Communications = communications
		options.CallLeases = &fakeCallLeases{}
		// Every optional service is present, so a refusal below comes from
		// the access check and not from "this build has no such service" --
		// which would answer 503 to everyone and prove nothing.
		options.Recording = &fakeRecordingService{}
		options.DeviceConfigurations = &fakeDeviceConfigurations{}
		options.CallPolicies = &fakeCallPolicies{}
		options.Network = &fakeNetworkService{}
		options.LineServices = &fakeLineServices{}
		options.CallMedia = &fakeCallMedia{}
	})
	harness.seedHistory()
	alice := harness.aliceClient()
	bob := harness.bobClient()
	create := func(client *advClient, name, number string) string {
		response := client.send(http.MethodPost, "/api/v1/contacts",
			`{"display_name":"`+name+`","phones":[{"label":"mobile","number":"`+number+`","primary":true}]}`)
		if response.Code != http.StatusCreated {
			t.Fatalf("create contact %s: %d %s", name, response.Code, response.Body)
		}
		var body contactResponse
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Contact.ID
	}
	aliceContact := create(alice, "adv alice contact", "+15555550121")
	bobContact := create(bob, "adv bob contact", "+15555550122")
	return harness, communications, alice, bob, aliceContact, bobContact
}

func TestAdversarialMemberCannotSeeAnotherMembersData(t *testing.T) {
	harness, _, alice, bob, _, bobContact := advCrossUserHarness(t)

	// Backups: bob writes one; it holds bob's address book.
	response := bob.send(http.MethodPost, "/api/v1/contacts/backup", "{}")
	advRequireStatus(t, response, http.StatusCreated, "")
	var backup struct {
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &backup); err != nil || backup.Filename == "" {
		t.Fatalf("backup response: %v %s", err, response.Body)
	}

	// Nothing of bob's may appear in anything alice can list.
	bobSessionID, _ := bob.accountSessionIDs()
	needles := []string{
		bobContact, "adv bob contact", "+15555550122", // bob's contact
		advLineBeta, advPeerBeta, "adv beta history", "adv-call-beta", // bob's line
		bobSessionID, backup.Filename, harness.bob.ID,
	}
	for _, path := range []string{
		"/api/v1/bootstrap",
		"/api/v1/contacts",
		"/api/v1/contacts?search=adv",
		"/api/v1/contacts/backup",
		"/api/v1/messages/threads",
		"/api/v1/messages/threads?search=adv",
		"/api/v1/calls",
		"/api/v1/calls?search=5555",
		"/api/v1/calls/active",
		"/api/v1/recordings",
		"/api/v1/devices",
		"/api/v1/account/sessions",
		"/api/v1/settings/lines",
		"/api/v1/stats/communication",
		"/api/v1/network",
		"/api/v1/proxies",
		"/api/v1/security/posture",
	} {
		response := alice.get(path)
		if response.Code >= 500 && response.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s: %d %s", path, response.Code, response.Body)
		}
		for _, needle := range needles {
			if strings.Contains(response.Body.String(), needle) {
				t.Errorf("GET %s as alice discloses %q, which belongs to bob", path, needle)
			}
		}
	}
	// The control: bob's own listings do contain them, so the needles are
	// real and the absence above is the scoping.
	for path, needle := range map[string]string{
		"/api/v1/contacts":         "adv bob contact",
		"/api/v1/contacts/backup":  backup.Filename,
		"/api/v1/messages/threads": "adv beta history",
		"/api/v1/calls":            "adv-call-beta",
		"/api/v1/account/sessions": bobSessionID,
	} {
		if response := bob.get(path); !strings.Contains(response.Body.String(), needle) {
			t.Errorf("GET %s as bob does not contain %q; the cross-user check proves nothing: %s",
				path, needle, response.Body)
		}
	}
	// The administrator owns neither address book.
	admin := harness.admin()
	for _, path := range []string{"/api/v1/contacts", "/api/v1/contacts/backup", "/api/v1/contacts/backup/" + backup.Filename} {
		response := admin.get(path)
		for _, needle := range []string{"adv bob contact", backup.Filename} {
			if strings.Contains(response.Body.String(), needle) {
				t.Errorf("GET %s as the administrator discloses bob's %q", path, needle)
			}
		}
	}
	// Bob can read his own backup back, with his contact in it.
	response = bob.get("/api/v1/contacts/backup/" + backup.Filename)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "adv bob contact") {
		t.Fatalf("bob cannot restore his own backup: %d %s", response.Code, response.Body)
	}
}

// A backup written before files carried an owner cannot be attributed, so it
// is the administrator's to see and nobody else's.
func TestAdversarialUnownedLegacyBackupIsAdministratorOnly(t *testing.T) {
	harness := newAdvHarness(t)
	if err := os.MkdirAll(harness.backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const name = "contacts-20250101T000000Z-legacy1.json"
	legacy := `{"format":"homesim-contacts-v1","created_at":"2025-01-01T00:00:00Z","contacts":[{"display_name":"adv legacy contact"}]}` + "\n"
	if err := os.WriteFile(harness.backupDir+"/"+name, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	// A file whose first contact tries to look like an owner stamp for alice.
	const forged = "contacts-20250102T000000Z-forged1.json"
	spoof := `{"format":"homesim-contacts-v1","created_at":"2025-01-02T00:00:00Z","contacts":[{"display_name":"x","owner_user_id":"` +
		harness.alice.ID + `"}]}` + "\n"
	if err := os.WriteFile(harness.backupDir+"/"+forged, []byte(spoof), 0o600); err != nil {
		t.Fatal(err)
	}

	alice := harness.aliceClient()
	if response := alice.get("/api/v1/contacts/backup"); strings.Contains(response.Body.String(), "contacts-2025") {
		t.Fatalf("a member is shown unowned backups: %s", response.Body)
	}
	for _, file := range []string{name, forged} {
		response := alice.get("/api/v1/contacts/backup/" + file)
		advRequireStatus(t, response, http.StatusNotFound, "not_found")
	}
	admin := harness.admin()
	if response := admin.get("/api/v1/contacts/backup"); !strings.Contains(response.Body.String(), name) {
		t.Fatalf("the administrator lost sight of a pre-existing backup: %s", response.Body)
	}
	response := admin.get("/api/v1/contacts/backup/" + name)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "adv legacy contact") {
		t.Fatalf("the administrator cannot read a pre-existing backup: %d %s", response.Code, response.Body)
	}
}

// ID guessing. For every route that takes an identifier, a member asking for
// another member's resource must get exactly the answer a made-up identifier
// gets: same status, same body. A different answer -- 403 against 404, or a
// different message -- tells the caller the resource exists.
func TestAdversarialForeignIdentifiersAreIndistinguishableFromMissingOnes(t *testing.T) {
	harness, communications, alice, bob, aliceContact, bobContact := advCrossUserHarness(t)

	response := bob.send(http.MethodPost, "/api/v1/contacts/backup", "{}")
	advRequireStatus(t, response, http.StatusCreated, "")
	var backup struct {
		Filename string `json:"filename"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &backup)
	bobSessionID, _ := bob.accountSessionIDs()
	_ = harness.mobileToken(harness.bob.ID)
	var bobCredential string
	if err := harness.db.QueryRow(`SELECT id FROM modemdeck_ios_pairing_credentials WHERE user_id = ?`,
		harness.bob.ID).Scan(&bobCredential); err != nil {
		t.Fatalf("read bob's pairing credential: %v", err)
	}

	const ghostLine = "line_adv_ghost"
	const ghostCall = "adv-call-ghost"
	contactBody := `{"display_name":"adv overwritten","phones":[{"label":"mobile","number":"+15555550199","primary":true}],"revision":1}`
	lineBody := func(line, peer string) string {
		return `{"line_id":"` + line + `","peer":"` + peer + `"}`
	}
	type probe struct {
		name    string
		method  string
		foreign string
		ghost   string
		// bodies differ only in the identifier when it travels in the body.
		foreignBody string
		ghostBody   string
	}
	samePath := func(name, method, template, foreignID, ghostID, body string) probe {
		return probe{
			name: name, method: method,
			foreign:     strings.ReplaceAll(template, "{id}", foreignID),
			ghost:       strings.ReplaceAll(template, "{id}", ghostID),
			foreignBody: body, ghostBody: body,
		}
	}
	probes := []probe{
		samePath("revoke web session", http.MethodDelete, "/api/v1/account/sessions/{id}", bobSessionID, "AAAAAAAAAAAAAAAAAAAAAA", ""),
		samePath("revoke phone session", http.MethodDelete, "/api/v1/account/sessions/ios-pairing-{id}", bobCredential, "adv-ghost-credential", ""),
		samePath("read contact", http.MethodGet, "/api/v1/contacts/{id}", bobContact, "adv-ghost-contact", ""),
		samePath("update contact", http.MethodPut, "/api/v1/contacts/{id}", bobContact, "adv-ghost-contact", contactBody),
		samePath("delete contact", http.MethodDelete, "/api/v1/contacts/{id}?revision=1", bobContact, "adv-ghost-contact", ""),
		samePath("read backup", http.MethodGet, "/api/v1/contacts/backup/{id}", backup.Filename, "contacts-20260101T000000Z-ghost1.json", ""),
		samePath("read messages", http.MethodGet, "/api/v1/messages?line_id={id}&peer=%2B15555550102", advLineBeta, ghostLine, ""),
		samePath("call lease", http.MethodPut, "/api/v1/calls/{id}/lease", "adv-call-beta", ghostCall, "{}"),
		samePath("call media", http.MethodPost, "/api/v1/calls/{id}/media", "adv-call-beta", ghostCall, `{"sdp":"adv"}`),
		samePath("call media ice", http.MethodPost, "/api/v1/calls/{id}/media/ice", "adv-call-beta", ghostCall, "{}"),
		samePath("hang up", http.MethodPost, "/api/v1/calls/{id}/hangup", "adv-call-beta", ghostCall, `{"request_id":"adv-req-1"}`),
		samePath("answer", http.MethodPost, "/api/v1/calls/{id}/answer", "adv-call-beta", ghostCall, `{"request_id":"adv-req-2"}`),
		samePath("dtmf", http.MethodPost, "/api/v1/calls/{id}/dtmf", "adv-call-beta", ghostCall, `{"request_id":"adv-req-3","digits":"1"}`),
		samePath("mark call read", http.MethodPost, "/api/v1/calls/{id}/read", "adv-call-beta", ghostCall, "{}"),
		samePath("mark call unread", http.MethodPost, "/api/v1/calls/{id}/unread", "adv-call-beta", ghostCall, "{}"),
		samePath("delete call", http.MethodDelete, "/api/v1/calls/{id}", "adv-call-beta", ghostCall, ""),
		samePath("call recordings", http.MethodGet, "/api/v1/calls/{id}/recordings", "adv-call-beta", ghostCall, ""),
		samePath("toggle recording", http.MethodPut, "/api/v1/calls/{id}/recording", "adv-call-beta", ghostCall, `{"enabled":true}`),
		samePath("download recording", http.MethodGet, "/api/v1/calls/{id}/recordings/adv-call-beta-rec/download", "adv-call-beta", ghostCall, ""),
		samePath("delete recording", http.MethodDelete, "/api/v1/calls/{id}/recordings/adv-call-beta-rec", "adv-call-beta", ghostCall, ""),
		samePath("device configuration", http.MethodGet, "/api/v1/devices/{id}/configuration", advLineBeta, ghostLine, ""),
		samePath("apply device configuration", http.MethodPatch, "/api/v1/devices/{id}/configuration", advLineBeta, ghostLine, "{}"),
		samePath("network selection", http.MethodGet, "/api/v1/devices/{id}/network-selection", advLineBeta, ghostLine, ""),
		samePath("network scan", http.MethodPost, "/api/v1/devices/{id}/network-scan", advLineBeta, ghostLine, "{}"),
		samePath("sim status", http.MethodGet, "/api/v1/devices/{id}/sim", advLineBeta, ghostLine, ""),
		samePath("sim command", http.MethodPost, "/api/v1/devices/{id}/sim/commands", advLineBeta, ghostLine, "{}"),
		samePath("sim auto unlock", http.MethodGet, "/api/v1/devices/{id}/sim/auto-unlock", advLineBeta, ghostLine, ""),
		samePath("connection profiles", http.MethodGet, "/api/v1/devices/{id}/profiles", advLineBeta, ghostLine, ""),
		samePath("ussd", http.MethodPost, "/api/v1/devices/{id}/ussd", advLineBeta, ghostLine, "{}"),
		samePath("call services", http.MethodGet, "/api/v1/devices/{id}/call-services", advLineBeta, ghostLine, ""),
		samePath("line label", http.MethodPatch, "/api/v1/lines/{id}/label", advLineBeta, ghostLine, `{"line_label":"adv taken"}`),
		{
			name: "send message", method: http.MethodPost,
			foreign: "/api/v1/messages", ghost: "/api/v1/messages",
			foreignBody: `{"request_id":"adv-m1","line_id":"` + advLineBeta + `","to":"+15555550102","content":"adv"}`,
			ghostBody:   `{"request_id":"adv-m1","line_id":"` + ghostLine + `","to":"+15555550102","content":"adv"}`,
		},
		{
			name: "start call", method: http.MethodPost,
			foreign: "/api/v1/calls", ghost: "/api/v1/calls",
			foreignBody: `{"request_id":"adv-c1","line_id":"` + advLineBeta + `","number":"+15555550102"}`,
			ghostBody:   `{"request_id":"adv-c1","line_id":"` + ghostLine + `","number":"+15555550102"}`,
		},
		{
			name: "mark thread read", method: http.MethodPatch,
			foreign: "/api/v1/messages/read", ghost: "/api/v1/messages/read",
			foreignBody: lineBody(advLineBeta, advPeerBeta), ghostBody: lineBody(ghostLine, advPeerBeta),
		},
		{
			name: "delete thread", method: http.MethodPatch,
			foreign: "/api/v1/messages/threads/state", ghost: "/api/v1/messages/threads/state",
			foreignBody: `{"action":"delete","threads":[` + lineBody(advLineBeta, advPeerBeta) + `]}`,
			ghostBody:   `{"action":"delete","threads":[` + lineBody(ghostLine, advPeerBeta) + `]}`,
		},
		{
			name: "set profile contact", method: http.MethodPut,
			foreign: "/api/v1/account/contact", ghost: "/api/v1/account/contact",
			foreignBody: `{"contact_id":"` + bobContact + `"}`, ghostBody: `{"contact_id":"adv-ghost-contact"}`,
		},
	}
	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			foreign := alice.do(advRequest{method: probe.method, path: probe.foreign, body: probe.foreignBody})
			ghost := alice.do(advRequest{method: probe.method, path: probe.ghost, body: probe.ghostBody})
			if foreign.Code >= 200 && foreign.Code < 300 {
				t.Fatalf("%s %s succeeded on another member's resource: %d %s",
					probe.method, probe.foreign, foreign.Code, foreign.Body)
			}
			if foreign.Code != ghost.Code || foreign.Body.String() != ghost.Body.String() {
				t.Fatalf("existence leak on %s %s:\n  another member's: %d %s  made up:          %d %s",
					probe.method, probe.foreign, foreign.Code, foreign.Body, ghost.Code, ghost.Body)
			}
			// The refusal has to be the access check. A 5xx, a 405 or a 400
			// would mean the request never reached it and the equality above
			// is vacuous.
			if foreign.Code != http.StatusNotFound {
				t.Fatalf("%s %s: status = %d %s; want the 404 of the ownership check",
					probe.method, probe.foreign, foreign.Code, foreign.Body)
			}
			t.Logf("%-28s -> %d %s", probe.name, foreign.Code, advErrorCode(foreign))
		})
	}

	// None of it had an effect.
	if sends, starts, actions := communications.effects(); sends+starts+actions != 0 {
		t.Fatalf("a cross-user request reached the hardware: sends=%d starts=%d actions=%d", sends, starts, actions)
	}
	if !bob.alive() {
		t.Fatal("alice revoked bob's session")
	}
	response = bob.get("/api/v1/contacts/" + bobContact)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "adv bob contact") {
		t.Fatalf("bob's contact was changed or removed by alice: %d %s", response.Code, response.Body)
	}
	if response := bob.get("/api/v1/messages/threads"); !strings.Contains(response.Body.String(), "adv beta history") {
		t.Fatalf("bob's thread was removed by alice: %s", response.Body)
	}
	if response := bob.get("/api/v1/calls"); !strings.Contains(response.Body.String(), "adv-call-beta") {
		t.Fatalf("bob's call record was removed by alice: %s", response.Body)
	}
	var credentials int
	_ = harness.db.QueryRow(`SELECT COUNT(*) FROM modemdeck_ios_pairing_credentials WHERE user_id = ?`,
		harness.bob.ID).Scan(&credentials)
	if credentials != 1 {
		t.Fatal("alice revoked bob's paired phone")
	}
	// Alice's own resources still answer, so the refusals above were about
	// ownership.
	advRequireStatus(t, alice.get("/api/v1/contacts/"+aliceContact), http.StatusOK, "")
}

// Batch routes take identifiers in the body. A batch that names another
// member's rows must leave them untouched.
func TestAdversarialBatchRoutesIgnoreForeignRows(t *testing.T) {
	_, _, alice, bob, _, bobContact := advCrossUserHarness(t)

	alice.send(http.MethodPatch, "/api/v1/contacts/batch",
		`{"action":"delete","contacts":[{"id":"`+bobContact+`","revision":1}]}`)
	alice.send(http.MethodPatch, "/api/v1/calls/batch", `{"action":"delete","ids":["adv-call-beta"]}`)
	alice.send(http.MethodPatch, "/api/v1/calls/batch", `{"action":"favorite","ids":["adv-call-beta"]}`)
	alice.send(http.MethodPatch, "/api/v1/recordings/batch",
		`{"action":"favorite","recordings":[{"call_id":"adv-call-beta","id":"adv-call-beta-rec"}]}`)
	alice.send(http.MethodPost, "/api/v1/calls/missed/read", "{}")

	advRequireStatus(t, bob.get("/api/v1/contacts/"+bobContact), http.StatusOK, "")
	response := bob.get("/api/v1/calls")
	if !strings.Contains(response.Body.String(), "adv-call-beta") {
		t.Fatalf("bob's call record was removed by alice's batch: %s", response.Body)
	}
	if strings.Contains(response.Body.String(), `"favorite":true`) {
		t.Fatalf("alice's batch changed bob's call record: %s", response.Body)
	}
}

// Account settings are per account: changing mine must not change yours.
func TestAdversarialAccountSettingsAreNotShared(t *testing.T) {
	harness, _, alice, bob, _, _ := advCrossUserHarness(t)

	read := func(client *advClient) (language string, revision int64) {
		response := client.get("/api/v1/settings/system")
		advRequireStatus(t, response, http.StatusOK, "")
		var body systemSettingsResponse
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return string(body.Settings.Language), body.Settings.Revision
	}
	bobLanguage, _ := read(bob)
	_, aliceRevision := read(alice)
	target := "ja-JP"
	if bobLanguage == target {
		target = "en-US"
	}
	body, _ := json.Marshal(map[string]any{"language": target, "expected_revision": aliceRevision})
	response := alice.send(http.MethodPatch, "/api/v1/settings/system", string(body))
	advRequireStatus(t, response, http.StatusOK, "")
	if language, _ := read(alice); language != target {
		t.Fatalf("alice's language = %q after setting %q", language, target)
	}
	if language, _ := read(bob); language != bobLanguage {
		t.Fatalf("alice changing her language changed bob's: %q -> %q", bobLanguage, language)
	}

	// A member cannot make a line they were not given their default.
	response = alice.get("/api/v1/settings/lines")
	advRequireStatus(t, response, http.StatusOK, "")
	var lines lineSettingsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &lines); err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(map[string]any{"default_line_id": advLineBeta, "expected_revision": lines.Settings.Revision})
	response = alice.send(http.MethodPatch, "/api/v1/settings/lines", string(body))
	if response.Code >= 200 && response.Code < 300 {
		t.Fatalf("alice set bob's line as her default: %d %s", response.Code, response.Body)
	}
	_ = harness
}

// A paired phone carries its owner's role and line scope, exactly like that
// user's browser, and bypasses nothing but the CSRF token (it holds no
// ambient cookie to forge with).
func TestAdversarialMobileBearerIsScopedLikeItsUser(t *testing.T) {
	harness, communications, _, _, _, bobContact := advCrossUserHarness(t)
	token := harness.mobileToken(harness.alice.ID)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		var request *http.Request
		if body == "" {
			request = httptest.NewRequest(method, path, nil)
		} else {
			request = httptest.NewRequest(method, path, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		harness.api.ServeHTTP(response, request)
		return response
	}
	advRequireStatus(t, call(http.MethodGet, "/api/v1/users", ""), http.StatusForbidden, "admin_required")
	advRequireStatus(t, call(http.MethodGet, "/api/v1/diagnostics/logs", ""), http.StatusForbidden, "admin_required")
	advRequireStatus(t, call(http.MethodGet, "/api/v1/homesim/f50/sms", ""), http.StatusForbidden, "admin_required")
	advRequireStatus(t, call(http.MethodGet, "/api/v1/contacts/"+bobContact, ""), http.StatusNotFound, "contact_not_found")
	advRequireStatus(t, call(http.MethodPost, "/api/v1/messages",
		`{"request_id":"adv-mobile-1","line_id":"`+advLineBeta+`","to":"+15555550102","content":"adv"}`),
		http.StatusNotFound, "line_not_found")
	// The web session endpoints are closed to a bearer credential.
	advRequireStatus(t, call(http.MethodDelete, "/api/v1/session", ""), http.StatusForbidden, "mobile_api_forbidden")
	if sends, _, _ := communications.effects(); sends != 0 {
		t.Fatal("a phone sent a message on a line its user was not given")
	}

	// A session cookie does not rescue a bad bearer token: the presence of an
	// Authorization header selects bearer authentication outright, so the
	// header cannot be used to step around the CSRF check on a cookie.
	alice := harness.aliceClient()
	response := alice.do(advRequest{
		method: http.MethodPut, path: "/api/v1/account/contact", body: `{"contact_id":""}`,
		noCSRFHeader: true,
		headers:      map[string]string{"Authorization": "Bearer not-a-token"},
	})
	advRequireStatus(t, response, http.StatusUnauthorized, "authentication_required")
}
