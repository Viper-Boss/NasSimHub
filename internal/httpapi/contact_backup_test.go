package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	for _, item := range []struct {
		method string
		status int
	}{{http.MethodPost, 503}, {http.MethodGet, 405}} {
		response := httptest.NewRecorder()
		api.ServeHTTP(response, httptest.NewRequest(item.method, "/api/v1/contacts/backup", nil))
		if response.Code != item.status {
			t.Fatalf("%s: %d", item.method, response.Code)
		}
	}
}
