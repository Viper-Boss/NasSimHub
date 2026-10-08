package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
)

type pqFleet struct {
	fakeNodeFleet
	reset []string
	err   error
}

func (f *pqFleet) ResetPQPin(_ context.Context, id string) (nassimhubnode.NodeView, error) {
	f.reset = append(f.reset, id)
	if f.err != nil {
		return nassimhubnode.NodeView{}, f.err
	}
	return nassimhubnode.NodeView{
		DeviceID: id, Online: true,
		Identity: nassimhubnode.IdentityView{Outcome: nassimhubnode.IdentityOutcomeDual, PinState: nassimhubnode.PinStatePinned,
			Algorithm: "ml-dsa-65", Fingerprint: "AAAA BBBB"},
	}, nil
}

func TestPQPinResetIsAdminOnlyTakesOnlyTheDeviceIDAndReturnsTheNewState(t *testing.T) {
	if !adminOnlyAPIPath("/api/v1/nodes/pq-pin/reset", http.MethodPost) {
		t.Fatal("the pin reset route must be administrator-only")
	}
	fleet := &pqFleet{}
	api := &API{nodeFleet: fleet}

	w := httptest.NewRecorder()
	api.nassimhubResetPQPin(w, jsonRequest(http.MethodPost, "/api/v1/nodes/pq-pin/reset", `{"device_id":"NSH-410-0A0B0C"}`))
	if w.Code != http.StatusOK || len(fleet.reset) != 1 || fleet.reset[0] != "NSH-410-0A0B0C" {
		t.Fatalf("reset: %d %v", w.Code, fleet.reset)
	}
	var body struct {
		Node struct {
			Identity struct {
				Outcome     string `json:"outcome"`
				PinState    string `json:"pin_state"`
				Fingerprint string `json:"fingerprint"`
			} `json:"identity"`
		} `json:"node"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Node.Identity.Outcome != "dual" || body.Node.Identity.PinState != "pinned" || body.Node.Identity.Fingerprint == "" {
		t.Fatalf("response identity: %+v", body.Node.Identity)
	}
	// The response never carries key material.
	if strings.Contains(w.Body.String(), "public_key") || strings.Contains(w.Body.String(), "seed") {
		t.Fatalf("the response carries key material: %s", w.Body.String())
	}

	// A key cannot be supplied by the caller, and unknown fields are refused.
	for _, bad := range []string{
		`{"device_id":"NSH-410-0A0B0C","public_key":"QUJD"}`,
		`{"device_id":""}`,
		`{}`,
		strings.Repeat("x", 4096),
	} {
		w = httptest.NewRecorder()
		api.nassimhubResetPQPin(w, jsonRequest(http.MethodPost, "/api/v1/nodes/pq-pin/reset", bad))
		if w.Code < 400 {
			t.Fatalf("request %q was accepted", bad[:min(len(bad), 40)])
		}
	}
	if len(fleet.reset) != 1 {
		t.Fatalf("a refused request reached the fleet: %v", fleet.reset)
	}

	fleet.err = nassimhubnode.ErrUnknownNode
	w = httptest.NewRecorder()
	api.nassimhubResetPQPin(w, jsonRequest(http.MethodPost, "/api/v1/nodes/pq-pin/reset", `{"device_id":"NSH-410-FFFFFF"}`))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown node: %d", w.Code)
	}

	// A fleet that cannot manage pins answers unavailable rather than panicking.
	plain := &API{nodeFleet: &fakeNodeFleet{}}
	w = httptest.NewRecorder()
	plain.nassimhubResetPQPin(w, jsonRequest(http.MethodPost, "/api/v1/nodes/pq-pin/reset", `{"device_id":"x"}`))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("fleet without pin management: %d", w.Code)
	}
}

func TestPairingErrorsAboutPostQuantumIdentityHaveStableCodes(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{
		{nassimhubnode.ErrNodeNeedsPQ, "pq_identity_required"},
		{errors.Join(errors.New("wrapped"), nassimhubnode.ErrNodeDemandsPQ), "core_pq_required"},
	} {
		w := httptest.NewRecorder()
		writeNodeError(&API{}, w, jsonRequest(http.MethodPost, "/api/v1/nodes/pair", `{}`), "pair", test.err)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), test.code) {
			t.Fatalf("%v: %d %s", test.err, w.Code, w.Body.String())
		}
	}
}
