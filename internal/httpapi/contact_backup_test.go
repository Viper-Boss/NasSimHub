package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/store"
)

func TestContactBackupWritesServerData(t *testing.T) {
	directory := t.TempDir()
	api, err := New(&fakeRepository{contacts: []store.Contact{{ID: "c1", DisplayName: "Test"}}}, Options{disableAuthentication: true,
		disableLicenseEnforcement: true, ContactBackupDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/contacts/backup", nil))
	if response.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		Filename string `json:"filename"`
		Count    int    `json:"count"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Count != 1 || filepath.Base(result.Filename) != result.Filename {
		t.Fatalf("invalid receipt: %+v", result)
	}
	data, err := os.ReadFile(filepath.Join(directory, result.Filename))
	if err != nil {
		t.Fatal(err)
	}
	var backup struct {
		Contacts []store.Contact `json:"contacts"`
	}
	if err := json.Unmarshal(data, &backup); err != nil {
		t.Fatal(err)
	}
	if len(backup.Contacts) != 1 || backup.Contacts[0].ID != "c1" {
		t.Fatal("wrong backup content")
	}
}

func TestContactBackupUnavailableAndMethod(t *testing.T) {
	api, err := New(&fakeRepository{}, Options{disableAuthentication: true})
	if err != nil {
		t.Fatal(err)
	}
	// GET is the listing now, so with no directory configured it reports the
	// same unavailability POST does rather than a method error. PUT is still
	// not a thing this collection does.
	for _, item := range []struct {
		method string
		status int
	}{{http.MethodPost, 503}, {http.MethodGet, 503}, {http.MethodPut, 405}} {
		response := httptest.NewRecorder()
		api.ServeHTTP(response, httptest.NewRequest(item.method, "/api/v1/contacts/backup", nil))
		if response.Code != item.status {
			t.Fatalf("%s: %d", item.method, response.Code)
		}
	}
}

// The restore half: list what was written, then read one back.
//
// The restore itself happens in the browser, through the same import path the
// vCard flow uses, so what has to hold on this side is that the listing is
// honest about what exists and that no request can read a file the server
// configuration did not put in that directory.

func TestContactBackupListAndRead(t *testing.T) {
	directory := t.TempDir()
	api, err := New(&fakeRepository{contacts: []store.Contact{{ID: "c1", DisplayName: "Test"}}}, Options{
		disableAuthentication: true, disableLicenseEnforcement: true, ContactBackupDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}

	// An operator who has never pressed the button gets an empty list, not an
	// error: nothing is wrong, there is simply nothing there.
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/contacts/backup", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("empty listing status %d: %s", response.Code, response.Body.String())
	}
	var empty struct {
		Backups []struct {
			Filename string `json:"filename"`
		} `json:"backups"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &empty); err != nil {
		t.Fatal(err)
	}
	if len(empty.Backups) != 0 {
		t.Fatalf("expected no backups, got %d", len(empty.Backups))
	}

	response = httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/contacts/backup", nil))
	if response.Code != http.StatusCreated {
		t.Fatalf("write status %d: %s", response.Code, response.Body.String())
	}
	var receipt struct {
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}

	// Files that are not this writer's output are not listed. A directory the
	// operator also drops things into must not offer them for reading.
	for _, name := range []string{"notes.txt", "contacts-bad.json", "contacts-20240101T010101Z-xx.json.bak"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	response = httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/contacts/backup", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("listing status %d", response.Code)
	}
	var listing struct {
		Backups []struct {
			Filename  string `json:"filename"`
			CreatedAt string `json:"created_at"`
			SizeBytes int64  `json:"size_bytes"`
		} `json:"backups"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Backups) != 1 || listing.Backups[0].Filename != receipt.Filename {
		t.Fatalf("listing is wrong: %+v", listing.Backups)
	}
	if listing.Backups[0].SizeBytes <= 0 || listing.Backups[0].CreatedAt == "" {
		t.Fatalf("listing lost its metadata: %+v", listing.Backups[0])
	}

	// And reading one back returns exactly what was written, so the client can
	// import it.
	response = httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/contacts/backup/"+receipt.Filename, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("read status %d: %s", response.Code, response.Body.String())
	}
	onDisk, err := os.ReadFile(filepath.Join(directory, receipt.Filename))
	if err != nil {
		t.Fatal(err)
	}
	if response.Body.String() != string(onDisk) {
		t.Fatal("the response is not the file that was written")
	}
	var restored struct {
		Format   string          `json:"format"`
		Contacts []store.Contact `json:"contacts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Format != "homesim-contacts-v1" || len(restored.Contacts) != 1 || restored.Contacts[0].ID != "c1" {
		t.Fatalf("restored payload is wrong: %+v", restored)
	}
}

func TestContactBackupReadRefusesAnythingButItsOwnFiles(t *testing.T) {
	directory := t.TempDir()
	outside := filepath.Join(filepath.Dir(directory), "outside-secret.json")
	if err := os.WriteFile(outside, []byte(`{"secret":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "notes.txt"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	api, err := New(&fakeRepository{}, Options{disableAuthentication: true,
		disableLicenseEnforcement: true, ContactBackupDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	// Traversal in every spelling that reaches a Go handler, plus names that
	// merely look close to the writer's. None of them may return a body.
	for _, name := range []string{
		"../outside-secret.json",
		"..%2Foutside-secret.json",
		"%2e%2e%2foutside-secret.json",
		"notes.txt",
		"contacts-20240101T010101Z-xx.json/../notes.txt",
		"/etc/passwd",
		"contacts-20240101T010101Z-xx.json", // well-formed, but not present
	} {
		response := httptest.NewRecorder()
		api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/contacts/backup/"+name, nil))
		if response.Code == http.StatusOK {
			t.Fatalf("%q was served: %s", name, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "secret") || strings.Contains(response.Body.String(), "private") {
			t.Fatalf("%q leaked file contents", name)
		}
	}
	// Writing through this path is not offered at all.
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/contacts/backup/contacts-20240101T010101Z-xx.json", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE on a backup returned %d", response.Code)
	}
}

// The containment check on its own.
//
// The router validates the name against the writer's exact shape before
// readContactBackup ever sees it, which means the handler's own check that the
// resolved path is a direct child of the backup directory is unreachable
// through ServeHTTP -- sabotaging it changed no test result, measured. It is
// still worth having: it is what makes a future loosening of that pattern
// survivable rather than immediately exploitable. So it is tested where it can
// actually be reached, by calling the handler directly with the kind of name a
// future caller might hand it.
func TestContactBackupHandlerRefusesAPathOutsideItsDirectory(t *testing.T) {
	directory := t.TempDir()
	outside := filepath.Join(filepath.Dir(directory), "outside-secret.json")
	if err := os.WriteFile(outside, []byte(`{"secret":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	api, err := New(&fakeRepository{}, Options{disableAuthentication: true,
		disableLicenseEnforcement: true, ContactBackupDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"../outside-secret.json",
		"nested/contacts-20240101T010101Z-xx.json",
		filepath.Join("..", filepath.Base(directory), "..", "outside-secret.json"),
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/contacts/backup/x", nil)
		api.readContactBackup(response, request, name)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%q returned %d: %s", name, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "secret") {
			t.Fatalf("%q leaked the file", name)
		}
	}
}
