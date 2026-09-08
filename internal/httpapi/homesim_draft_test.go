package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestHomeSIMDraftValidation(t *testing.T) {
	if !validHomeSIMDraft(defaultHomeSIMDraft()) {
		t.Fatal("default invalid")
	}
	for _, endpoint := range []string{"http://example.com", "wss://user:secret@example.com", "https://example.com?key=secret", "wss://example.com#secret"} {
		draft := defaultHomeSIMDraft()
		draft.AI.Endpoint = endpoint
		if validHomeSIMDraft(draft) {
			t.Fatal("credential-bearing or insecure URL accepted")
		}
	}
	draft := defaultHomeSIMDraft()
	draft.AI.CredentialRef = "sk-secret"
	if validHomeSIMDraft(draft) {
		t.Fatal("raw secret accepted")
	}
	draft = defaultHomeSIMDraft()
	draft.Spam.Blacklist = []string{"10086"}
	draft.Spam.Whitelist = []string{"10086"}
	if validHomeSIMDraft(draft) {
		t.Fatal("conflicting lists accepted")
	}
}

func TestHomeSIMDraftPersistsAndConflicts(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "backups", "contacts")
	api, err := New(&fakeRepository{}, Options{disableAuthentication: true, ContactBackupDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	draft := defaultHomeSIMDraft()
	draft.AI.Model = "test-model"
	body, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	put := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		api.homeSIMDraft(response, newHomeSIMJSONRequest(http.MethodPut, "/api/v1/settings/homesim", bytes.NewReader(body)))
		return response
	}
	first := put()
	if first.Code != 200 {
		t.Fatalf("save: %d %s", first.Code, first.Body.String())
	}
	if put().Code != 409 {
		t.Fatal("stale revision accepted")
	}
	reopened, err := New(&fakeRepository{}, Options{disableAuthentication: true, ContactBackupDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	reopened.homeSIMDraft(response, httptest.NewRequest(http.MethodGet, "/api/v1/settings/homesim", nil))
	var result struct {
		Draft        homeSIMIntegrationDraft `json:"draft"`
		RuntimeReady bool                    `json:"runtime_ready"`
		State        string                  `json:"state"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Draft.AI.Model != "test-model" || result.Draft.Revision != 2 || result.RuntimeReady || result.State != "draft_only" {
		t.Fatal("bad saved draft or incorrectly advertised executor")
	}
	if !adminOnlyAPIPath("/api/v1/settings/homesim", http.MethodGet) || !adminOnlyAPIPath("/api/v1/settings/homesim", http.MethodPut) {
		t.Fatal("configuration must be administrator-only")
	}
}
