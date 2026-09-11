package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/store"
)

func newPeerNotesAPI(t *testing.T, repository *fakeRepository) *API {
	t.Helper()
	api, err := New(repository, Options{
		disableAuthentication:     true,
		disableLicenseEnforcement: true,
		ContactBackupDirectory:    filepath.Join(t.TempDir(), "backups", "contacts"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return api
}

func postPeerNote(t *testing.T, api *API, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/nkn/chat/peers", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	api.nknChatPeersHandler(recorder, request)
	return recorder
}

func peerNotes(t *testing.T, api *API) []nknChatPeerNote {
	t.Helper()
	recorder := httptest.NewRecorder()
	api.nknChatPeersHandler(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/nkn/chat/peers", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Peers []nknChatPeerNote `json:"peers"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Peers
}

const peerAddress = "peer." + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestNKNChatPeerNoteRoundTrip(t *testing.T) {
	api := newPeerNotesAPI(t, &fakeRepository{contact: store.Contact{ID: "c-1", DisplayName: "小王"}})

	if recorder := postPeerNote(t, api, `{"address":"`+peerAddress+`","alias":"小王的手机","contact_id":"c-1"}`); recorder.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", recorder.Code, recorder.Body.String())
	}
	notes := peerNotes(t, api)
	if len(notes) != 1 || notes[0].Alias != "小王的手机" || notes[0].ContactID != "c-1" {
		t.Fatalf("stored note = %+v", notes)
	}
	if notes[0].UpdatedAt == 0 {
		t.Fatal("note has no timestamp")
	}

	// Clearing both fields removes the entry rather than leaving a blank note
	// behind, so the address falls back to its short form in the UI.
	if recorder := postPeerNote(t, api, `{"address":"`+peerAddress+`","alias":"","contact_id":""}`); recorder.Code != http.StatusOK {
		t.Fatalf("clear status = %d", recorder.Code)
	}
	if notes := peerNotes(t, api); len(notes) != 0 {
		t.Fatalf("note survived clearing: %+v", notes)
	}
}

// A link to a contact that has since been deleted would leave a conversation
// with no name at all, which looks like data loss to the user.
func TestNKNChatPeerNoteRejectsUnknownContact(t *testing.T) {
	api := newPeerNotesAPI(t, &fakeRepository{contactError: errors.New("not found")})

	recorder := postPeerNote(t, api, `{"address":"`+peerAddress+`","alias":"x","contact_id":"missing"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", recorder.Code)
	}
	if notes := peerNotes(t, api); len(notes) != 0 {
		t.Fatalf("rejected note was still written: %+v", notes)
	}
}

func TestNKNChatPeerNoteValidatesInput(t *testing.T) {
	api := newPeerNotesAPI(t, &fakeRepository{})

	if recorder := postPeerNote(t, api, `{"address":"nodots","alias":"x"}`); recorder.Code != http.StatusBadRequest {
		t.Fatalf("address without a dot: status = %d; want 400", recorder.Code)
	}
	long := strings.Repeat("名", nknChatAliasMaxLength+1)
	if recorder := postPeerNote(t, api, `{"address":"`+peerAddress+`","alias":"`+long+`"}`); recorder.Code != http.StatusBadRequest {
		t.Fatalf("over-long alias: status = %d; want 400", recorder.Code)
	}
}

// A corrupt file must not be silently replaced: the aliases in it are the only
// copy the user has, and a rewrite would destroy them for good.
func TestNKNChatPeerNotesPreserveCorruptFile(t *testing.T) {
	api := newPeerNotesAPI(t, &fakeRepository{})
	path, ok := api.nknChatPeersPath()
	if !ok {
		t.Fatal("no peer note path")
	}
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if recorder := postPeerNote(t, api, `{"address":"`+peerAddress+`","alias":"x"}`); recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", recorder.Code)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{ this is not json" {
		t.Fatalf("corrupt file was overwritten: %q", raw)
	}
}
