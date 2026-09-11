package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/store"
)

func TestPhoneGeoHandler(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "backups", "contacts")
	api, err := New(&fakeRepository{}, Options{disableAuthentication: true,
		disableLicenseEnforcement: true, ContactBackupDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	api.homeSIMPhoneGeo(response, httptest.NewRequest(http.MethodGet, "/api/v1/homesim/phone-geo?number=13800138000", nil))
	if response.Code != 200 {
		t.Fatalf("phone-geo: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		Province string `json:"province"`
		Carrier  string `json:"carrier"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Province == "" || result.Carrier == "" {
		t.Fatalf("incomplete geo payload: %s", response.Body.String())
	}

	missing := httptest.NewRecorder()
	api.homeSIMPhoneGeo(missing, httptest.NewRequest(http.MethodGet, "/api/v1/homesim/phone-geo?number=10086", nil))
	if missing.Code != 404 {
		t.Fatalf("short code lookup should be 404, got %d", missing.Code)
	}
}

func TestLineQuotaPersistAndAttach(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "backups", "contacts")
	api, err := New(&fakeRepository{}, Options{disableAuthentication: true,
		disableLicenseEnforcement: true, ContactBackupDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	invalid := `{"revision":1,"quotas":{"line-a":{"data_used":-1,"data_total":0}}}`
	response := httptest.NewRecorder()
	api.homeSIMQuotas(response, newHomeSIMJSONRequest(http.MethodPut, "/api/v1/settings/homesim/quotas", bytes.NewReader([]byte(invalid))))
	if response.Code != 400 {
		t.Fatalf("negative quota accepted: %d", response.Code)
	}

	valid := `{"revision":1,"quotas":{"line-a":{"data_used":18.6,"data_total":20,"sms_used":118,"sms_total":200,"voice_used":132,"voice_total":500}}}`
	response = httptest.NewRecorder()
	api.homeSIMQuotas(response, newHomeSIMJSONRequest(http.MethodPut, "/api/v1/settings/homesim/quotas", bytes.NewReader([]byte(valid))))
	if response.Code != 200 {
		t.Fatalf("quota save: %d %s", response.Code, response.Body.String())
	}
	stale := httptest.NewRecorder()
	api.homeSIMQuotas(stale, newHomeSIMJSONRequest(http.MethodPut, "/api/v1/settings/homesim/quotas", bytes.NewReader([]byte(valid))))
	if stale.Code != 409 {
		t.Fatal("stale revision accepted")
	}

	quotas := api.loadLineQuotaMap()
	if len(quotas) != 1 {
		t.Fatalf("quota map size %d", len(quotas))
	}
	if quotas["line-a"].DataTotal != 20 {
		t.Fatalf("data total %v", quotas["line-a"].DataTotal)
	}

	summary := lineSummaryResponseFromStore(store.LineSummary{ID: "line-a"})
	attached := attachLineQuotas([]lineSummaryResponse{summary}, quotas)
	if attached[0].Quota == nil || attached[0].Quota.SmsTotal != 200 {
		t.Fatal("quota not attached to line summary")
	}
}

func TestCallFilterValidationAndPersistence(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "backups", "contacts")
	api, err := New(&fakeRepository{}, Options{disableAuthentication: true,
		disableLicenseEnforcement: true, ContactBackupDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	if !validHomeSIMCallFilter(defaultHomeSIMCallFilter()) {
		t.Fatal("default filter invalid")
	}
	bad := defaultHomeSIMCallFilter()
	bad.Mode = "whitelist"
	bad.Whitelist = []string{"not-a-number"}
	if validHomeSIMCallFilter(bad) {
		t.Fatal("invalid phone number accepted in whitelist")
	}
	bad = defaultHomeSIMCallFilter()
	bad.Mode = "blacklist"
	bad.Blacklist = []string{"13901898834", "13901898834"}
	if validHomeSIMCallFilter(bad) {
		t.Fatal("duplicate blacklist entries accepted")
	}

	body := `{"revision":1,"mode":"blacklist","whitelist":[],"blacklist":["13901898834"],"min_reports":10}`
	save := httptest.NewRecorder()
	api.homeSIMCallFilter(save, newHomeSIMJSONRequest(http.MethodPut, "/api/v1/settings/homesim/call-filter", bytes.NewReader([]byte(body))))
	if save.Code != 200 {
		t.Fatalf("filter save: %d %s", save.Code, save.Body.String())
	}
	reopened, err := New(&fakeRepository{}, Options{disableAuthentication: true,
		disableLicenseEnforcement: true, ContactBackupDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	load := httptest.NewRecorder()
	reopened.homeSIMCallFilter(load, httptest.NewRequest(http.MethodGet, "/api/v1/settings/homesim/call-filter", nil))
	if load.Code != 200 {
		t.Fatalf("filter load: %d", load.Code)
	}
	var result struct {
		Filter       homeSIMCallFilter `json:"filter"`
		RuntimeReady bool              `json:"runtime_ready"`
	}
	if err := json.Unmarshal(load.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Filter.Mode != "blacklist" || len(result.Filter.Blacklist) != 1 {
		t.Fatalf("filter not persisted: %s", load.Body.String())
	}
	if result.RuntimeReady {
		t.Fatal("call filter must stay draft_only until a call source is connected")
	}
}

func TestF50SettingsPersistAndValidate(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "backups", "contacts")
	api, err := New(&fakeRepository{}, Options{disableAuthentication: true,
		disableLicenseEnforcement: true, ContactBackupDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	bad := `{"revision":1,"base_url":"http://192.168.0.1/","username":"admin"}`
	save := httptest.NewRecorder()
	api.homeSIMF50(save, newHomeSIMJSONRequest(http.MethodPut, "/api/v1/settings/homesim/f50", bytes.NewReader([]byte(bad))))
	if save.Code != 400 {
		t.Fatalf("trailing slash base_url accepted: %d", save.Code)
	}
	good := `{"revision":1,"base_url":"http://192.168.0.1","username":"admin"}`
	save = httptest.NewRecorder()
	api.homeSIMF50(save, newHomeSIMJSONRequest(http.MethodPut, "/api/v1/settings/homesim/f50", bytes.NewReader([]byte(good))))
	if save.Code != 200 {
		t.Fatalf("f50 save: %d %s", save.Code, save.Body.String())
	}
	reopened, err := New(&fakeRepository{}, Options{disableAuthentication: true,
		disableLicenseEnforcement: true, ContactBackupDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	load := httptest.NewRecorder()
	reopened.homeSIMF50(load, httptest.NewRequest(http.MethodGet, "/api/v1/settings/homesim/f50", nil))
	var result struct {
		Settings    homeSIMF50Settings `json:"settings"`
		PasswordSet bool               `json:"password_set"`
	}
	if err := json.Unmarshal(load.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Settings.BaseURL != "http://192.168.0.1" {
		t.Fatalf("f50 settings not persisted: %s", load.Body.String())
	}
}
