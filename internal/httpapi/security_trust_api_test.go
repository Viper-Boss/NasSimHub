package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/platform/database"
	"github.com/human-agent65535/modemdeck/internal/trustengine"
)

type trustFleet struct {
	fakeNodeFleet
	engine *trustengine.Engine
}

func (f *trustFleet) Trust() *trustengine.Engine { return f.engine }

func newTrustAPI(t *testing.T) (*API, *trustengine.Engine, func(string, ...any)) {
	t.Helper()
	db, err := database.Open(context.Background(), database.Config{TargetPath: filepath.Join(t.TempDir(), "modemdeck.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := trustengine.NewSQLStore(db)
	if err != nil {
		t.Fatal(err)
	}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	engine, err := trustengine.New(context.Background(), trustengine.Options{Store: store, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Observe(context.Background(), trustengine.Reading{SubjectID: "NSH-410-AAAAAA", IdentityHash: "h", SIMHash: "s", Healthy: true}); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, arguments ...any) {
		t.Helper()
		if _, err := db.Exec(query, arguments...); err != nil {
			t.Fatal(err)
		}
	}
	return &API{nodeFleet: &trustFleet{engine: engine}}, engine, exec
}

func TestSecurityTrustRoutesAreAdminOnly(t *testing.T) {
	for _, path := range []string{"/api/v1/security/trust", "/api/v1/security/trust/restrict", "/api/v1/security/trust/quarantine",
		"/api/v1/security/trust/restore", "/api/v1/security/journal", "/api/v1/security/journal/verify", "/api/v1/security/journal/reseal"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			if !adminOnlyAPIPath(path, method) {
				t.Fatalf("%s %s must be administrator-only", method, path)
			}
		}
	}
	// The household-facing posture card stays reachable by members.
	if adminOnlyAPIPath("/api/v1/security/posture", http.MethodGet) {
		t.Fatal("the posture card is not administrator-only")
	}
}

func TestSecurityTrustShowsStateEvidenceAndTakesReasonedActions(t *testing.T) {
	api, _, _ := newTrustAPI(t)
	w := httptest.NewRecorder()
	api.securityTrust(w, httptest.NewRequest(http.MethodGet, "/api/v1/security/trust", nil))
	var document struct {
		Enforcement string `json:"enforcement"`
		Subjects    []struct {
			NodeID    string          `json:"node_id"`
			State     string          `json:"state"`
			Effective string          `json:"effective_state"`
			Remaining int             `json:"remaining_seconds"`
			Allowed   map[string]bool `json:"allowed"`
		} `json:"subjects"`
		Journal  struct{ OK bool } `json:"journal"`
		Evidence map[string]string `json:"evidence"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &document); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %v %s", w.Code, err, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" || document.Enforcement != "enforce" || !document.Journal.OK {
		t.Fatalf("document: %s", w.Body.String())
	}
	subject := document.Subjects[0]
	if subject.State != "OBSERVATION" || subject.Remaining != 7*24*3600 || subject.Allowed["dial"] || !subject.Allowed["receive_sms"] {
		t.Fatalf("subject: %+v", subject)
	}
	// Inputs the engine has no evidence for are named as such, never as fine.
	if document.Evidence["modem_change"] != "unsupported" || document.Evidence["location"] != "not_used" || document.Evidence["node_local"] != "unsupported" {
		t.Fatalf("evidence: %+v", document.Evidence)
	}
	// Hashes and signatures never leave the gateway.
	for _, secret := range []string{"binding_hash", "sim_hash", "signature", "last_event_hash"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("the response exposes %s", secret)
		}
	}

	post := func(handler http.HandlerFunc, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler(w, jsonRequest(http.MethodPost, path, body))
		return w
	}
	for _, bad := range []string{`{"device_id":"NSH-410-AAAAAA"}`, `{"reason":"x"}`, `{"device_id":"NSH-410-AAAAAA","reason":""}`,
		`{"device_id":"NSH-410-AAAAAA","reason":"x","state":"TRUSTED"}`, `{"device_id":"NSH-410-AAAAAA","reason":"` + strings.Repeat("r", 201) + `"}`} {
		if w := post(api.securityTrustRestrict, "/api/v1/security/trust/restrict", bad); w.Code != http.StatusBadRequest {
			t.Fatalf("accepted %s: %d", bad, w.Code)
		}
	}
	if w := post(api.securityTrustRestrict, "/api/v1/security/trust/restrict", `{"device_id":"NSH-410-NOBODY","reason":"x"}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown device: %d", w.Code)
	}
	if w := post(api.securityTrustQuarantine, "/api/v1/security/trust/quarantine", `{"device_id":"NSH-410-AAAAAA","reason":"lost"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"QUARANTINE"`) {
		t.Fatalf("quarantine: %d %s", w.Code, w.Body.String())
	}
	if w := post(api.securityTrustRestore, "/api/v1/security/trust/restore", `{"device_id":"NSH-410-AAAAAA","reason":"found"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"OBSERVATION"`) {
		t.Fatalf("restore leads to observation: %d %s", w.Code, w.Body.String())
	}
	if w := post(api.securityTrustRestore, "/api/v1/security/trust/restore", `{"device_id":"NSH-410-AAAAAA","reason":"again"}`); w.Code != http.StatusConflict {
		t.Fatalf("restoring a device that is not restricted: %d", w.Code)
	}

	w = httptest.NewRecorder()
	api.securityJournal(w, httptest.NewRequest(http.MethodGet, "/api/v1/security/journal?node_id=NSH-410-AAAAAA&limit=10", nil))
	var journal struct {
		Events []struct {
			Seq                         int64
			Kind, Cause, Source, Result string
			At                          time.Time
		} `json:"events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &journal); err != nil || len(journal.Events) != 3 {
		t.Fatalf("journal: %v %s", err, w.Body.String())
	}
	last := journal.Events[2]
	if last.Kind != "MANUAL_RESTORE" || last.Cause != "found" || !strings.Contains(last.Source, "administrator") || last.Result == "" || last.At.IsZero() {
		t.Fatalf("an event explains cause, source, time and result: %+v", last)
	}
	w = httptest.NewRecorder()
	api.securityJournal(w, httptest.NewRequest(http.MethodGet, "/api/v1/security/journal?order=desc&limit=2", nil))
	if err := json.Unmarshal(w.Body.Bytes(), &journal); err != nil || len(journal.Events) != 2 || journal.Events[0].Kind != "MANUAL_RESTORE" || journal.Events[0].Seq <= journal.Events[1].Seq {
		t.Fatalf("newest first: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	api.securityJournal(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/security/journal?before=%d", journal.Events[1].Seq), nil))
	if err := json.Unmarshal(w.Body.Bytes(), &journal); err != nil || len(journal.Events) != 1 || journal.Events[0].Kind != "PAIRING_BOUND" {
		t.Fatalf("paging backwards: %s", w.Body.String())
	}
	for _, bad := range []string{"after=-1", "after=x", "limit=0", "limit=501", "before=0", "before=x"} {
		w = httptest.NewRecorder()
		api.securityJournal(w, httptest.NewRequest(http.MethodGet, "/api/v1/security/journal?"+bad, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", bad, w.Code)
		}
	}
}

func TestSecurityJournalDamageIsReportedAndResealedOnlyWithAReason(t *testing.T) {
	api, _, exec := newTrustAPI(t)
	post := func(handler http.HandlerFunc, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler(w, jsonRequest(http.MethodPost, path, body))
		return w
	}
	if w := post(api.securityJournalReseal, "/api/v1/security/journal/reseal", `{"reason":"nothing wrong"}`); w.Code != http.StatusConflict {
		t.Fatalf("an intact journal cannot be resealed: %d", w.Code)
	}
	exec(`UPDATE security_events SET result = 'trusted' WHERE seq = 1`)
	w := post(api.securityJournalVerify, "/api/v1/security/journal/verify", `{}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":false`) || !strings.Contains(w.Body.String(), `"first_bad_seq":1`) {
		t.Fatalf("verify must report the damage and where: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	api.securityTrust(w, httptest.NewRequest(http.MethodGet, "/api/v1/security/trust", nil))
	if !strings.Contains(w.Body.String(), `"effective_state":"RESTRICTED"`) {
		t.Fatalf("a damaged journal restricts: %s", w.Body.String())
	}
	if w := post(api.securityJournalReseal, "/api/v1/security/journal/reseal", `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a reseal needs a reason: %d", w.Code)
	}
	if w := post(api.securityJournalReseal, "/api/v1/security/journal/reseal", `{"reason":"restored from backup"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("reseal: %d %s", w.Code, w.Body.String())
	}
}

func TestSecurityTrustWithoutAnEngineIsUnavailableNotEmpty(t *testing.T) {
	for _, api := range []*API{{nodeFleet: &fakeNodeFleet{}}, {nodeFleet: &trustFleet{}}, {}} {
		w := httptest.NewRecorder()
		api.securityTrust(w, httptest.NewRequest(http.MethodGet, "/api/v1/security/trust", nil))
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "trust_unavailable") {
			t.Fatalf("no engine: %d %s", w.Code, w.Body.String())
		}
	}
}
