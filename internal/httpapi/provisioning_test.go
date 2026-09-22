package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/provision"
)

// clonedAPI forces the clone verdict.
//
// The decision is normally derived from hardware this container does not have,
// and an earlier version of a test in this package skipped when the fingerprint
// was unreadable -- which meant it never ran anywhere it mattered. Setting the
// resolved decision directly keeps the behaviour under test without depending
// on the machine the tests run on.
func clonedAPI(t *testing.T) (*API, string) {
	t.Helper()
	api, _, directory := licensedAPI(t)
	// The directory must EXIST, or RequestReset fails for an unrelated reason
	// and every test below passes without exercising what it names. That is how
	// the confirmation test first passed while the confirmation check was
	// removed.
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	api.provisionOnce.Do(func() {}) // resolved
	api.provisionDecision = provision.Decision{
		Status:           provision.StatusCloned,
		Reason:           "这份数据来自另一台设备",
		StoredMachineID:  "machine-elsewhere",
		CurrentMachineID: "machine-here",
		DivergingSources: []string{"mmc-cid", "dt-serial"},
	}
	return api, directory
}

// THE test. A gateway carrying somebody else's data directory must not accept
// the password that came with it.
//
// Without this, whoever prepared a preloaded image can log into every gateway
// they ever sold, using credentials the buyer was never told and cannot see to
// change.
func TestACloneRefusesTheInheritedPassword(t *testing.T) {
	api, _ := clonedAPI(t)

	body := strings.NewReader(`{"username":"admin","password":"whatever-was-baked-in"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/session", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)

	if response.Code == http.StatusOK {
		t.Fatalf("a cloned gateway accepted a login: %d %s", response.Code, response.Body)
	}
	if response.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 so the client can tell this from a wrong password", response.Code)
	}
	if !strings.Contains(response.Body.String(), "provision_required") {
		t.Errorf("the refusal does not name the cause: %s", response.Body)
	}
}

// Setting up a new administrator over the inherited database would leave the
// previous owner's messages, NKN identity and tokens in place under a new
// password.
func TestACloneRefusesSetupUntilReset(t *testing.T) {
	api, _ := clonedAPI(t)

	body := strings.NewReader(`{"username":"newowner","password":"a-good-password"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/setup", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)

	if response.Code == http.StatusOK {
		t.Fatalf("setup succeeded over a cloned directory: %s", response.Body)
	}
	if !strings.Contains(response.Body.String(), "provision_required") {
		t.Errorf("unexpected refusal: %d %s", response.Code, response.Body)
	}
}

// The session endpoint has to say what is wrong, or the login page can only
// show "something went wrong".
func TestTheSessionReportsACloneWithoutConsultingTheInheritedAccount(t *testing.T) {
	api, _ := clonedAPI(t)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/session", nil)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	var session sessionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if !session.ProvisionRequired {
		t.Fatal("a cloned gateway did not report provision_required")
	}
	if session.Authenticated {
		t.Error("a cloned gateway reported an authenticated session")
	}
	if session.SetupRequired {
		t.Error("provision_required and setup_required are different screens and must not both be set")
	}
	if strings.TrimSpace(session.ProvisionReason) == "" {
		t.Error("no reason given; the login page has nothing to show")
	}
}

// A healthy gateway must have no reachable wipe endpoint at all.
func TestAHealthyGatewayCannotBeReset(t *testing.T) {
	api, _, directory := licensedAPI(t)

	body := strings.NewReader(`{"confirm_machine_id":"anything"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/provision/reset", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("a healthy gateway answered the reset endpoint with %d: %s",
			response.Code, response.Body)
	}
	if _, pending := provision.ResetPending(directory); pending {
		t.Fatal("a healthy gateway recorded a reset request")
	}
}

// The endpoint cannot require a login -- the inherited one is refused -- so the
// confirmation is what keeps it from being a drive-by wipe.
func TestTheResetNeedsTheMachineIDTyped(t *testing.T) {
	api, directory := clonedAPI(t)

	for name, payload := range map[string]string{
		"empty body":  `{}`,
		"wrong value": `{"confirm_machine_id":"machine-elsewhere"}`,
		"whitespace":  `{"confirm_machine_id":"   "}`,
		"near miss":   `{"confirm_machine_id":"machine-her"}`,
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/provision/reset",
				strings.NewReader(payload))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			api.ServeHTTP(response, request)

			if response.Code == http.StatusAccepted {
				t.Fatalf("an unconfirmed reset was accepted: %s", response.Body)
			}
			if _, pending := provision.ResetPending(directory); pending {
				t.Fatal("an unconfirmed request wrote the marker")
			}
		})
	}
}

// And the confirmed path records the marker rather than deleting anything now.
func TestAConfirmedResetRecordsAMarkerAndDeletesNothingYet(t *testing.T) {
	api, directory := clonedAPI(t)

	// Something to lose, so "deletes nothing yet" is a real assertion.
	witness := filepath.Join(directory, "modemdeck.db")
	if err := os.WriteFile(witness, []byte("the previous owner's data"), 0o600); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/provision/reset",
		strings.NewReader(`{"confirm_machine_id":"machine-here"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", response.Code, response.Body)
	}
	request2, pending := provision.ResetPending(directory)
	if !pending {
		t.Fatal("no marker was written, so the restart would do nothing")
	}
	if request2.StoredMachineID != "machine-elsewhere" {
		t.Errorf("the marker does not record whose data is being erased: %+v", request2)
	}
	// Nothing removed yet: the wipe belongs to the next boot, before the
	// database is opened.
	if _, err := os.Stat(witness); err != nil {
		t.Fatalf("the reset deleted files while the gateway was running: %v", err)
	}

	// And the boot-time half finishes the job.
	if err := provision.Reset(directory); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(witness); !os.IsNotExist(err) {
		t.Fatalf("the boot-time reset left the database behind: %v", err)
	}
}

// The status endpoint is readable without a session, because the login page
// needs it before anyone can log in.
func TestTheProvisionStatusIsReadableWithoutASession(t *testing.T) {
	api, _ := clonedAPI(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/provision", nil)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["cloned"] != true {
		t.Errorf("cloned = %v", payload["cloned"])
	}
	if payload["machine_id"] != "machine-here" {
		t.Errorf("machine_id = %v; the page cannot show what to type", payload["machine_id"])
	}
}
