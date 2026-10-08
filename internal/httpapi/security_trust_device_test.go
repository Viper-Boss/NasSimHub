package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/modemdeck/internal/trustengine"
)

// deviceFleet is a fleet whose devices report what a test says they report.
type deviceFleet struct {
	trustFleet
	devices map[string]nassimhubnode.DeviceEnforcementView
}

func (f *deviceFleet) Nodes() []nassimhubnode.NodeView {
	views := make([]nassimhubnode.NodeView, 0, len(f.devices))
	for id, device := range f.devices {
		views = append(views, nassimhubnode.NodeView{DeviceID: id, Online: true, Trust: nassimhubnode.TrustView{Known: true, Device: device}})
	}
	return views
}

type trustDeviceDocument struct {
	Subjects []struct {
		NodeID           string                              `json:"node_id"`
		State            string                              `json:"state"`
		PolicyGeneration uint64                              `json:"policy_generation"`
		ModemChanges     int                                 `json:"modem_changes"`
		Device           nassimhubnode.DeviceEnforcementView `json:"device"`
		Evidence         map[string]string                   `json:"evidence"`
	} `json:"subjects"`
	Evidence map[string]string `json:"evidence"`
}

// "NAS 显示状态" and "设备已执行" are two fields, and neither is filled in from
// the other.
func TestSecurityTrustKeepsTheGatewayDecisionAndTheDeviceReportApart(t *testing.T) {
	api, engine, _ := newTrustAPI(t)
	ctx := context.Background()
	for _, id := range []string{"NSH-410-BBBBBB", "NSH-410-CCCCCC", "NSH-410-DDDDDD"} {
		if err := engine.Observe(ctx, trustengine.Reading{SubjectID: id, IdentityHash: "h-" + id, Healthy: true}); err != nil {
			t.Fatal(err)
		}
	}
	checked := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	fleet := &deviceFleet{trustFleet: trustFleet{engine: engine}, devices: map[string]nassimhubnode.DeviceEnforcementView{
		// Holds and enforces what the gateway signed, and reports hardware.
		"NSH-410-AAAAAA": {Known: true, Supported: true, Installed: true, Generation: 3, State: "OBSERVATION", MatchesGateway: true, Enforcing: true, CheckedAt: checked, GatewayGeneration: 3, HardwareReported: true},
		// An agent that predates enforcement.
		"NSH-410-BBBBBB": {Known: true, Supported: false, Reason: "the device's agent predates device-local enforcement"},
		// Supports it, holds an older policy, reports no hardware identifier.
		"NSH-410-CCCCCC": {Known: true, Supported: true, Installed: true, Generation: 1, State: "TRUSTED", Stale: true, Enforcing: true, GatewayGeneration: 2, HardwareReason: "the SoC serial number is all zeros"},
		// NSH-410-DDDDDD has a record and is not paired any more.
	}}
	api.nodeFleet = fleet
	w := httptest.NewRecorder()
	api.securityTrust(w, httptest.NewRequest(http.MethodGet, "/api/v1/security/trust", nil))
	var document trustDeviceDocument
	if err := json.Unmarshal(w.Body.Bytes(), &document); err != nil || w.Code != http.StatusOK || len(document.Subjects) != 4 {
		t.Fatalf("%d %v %s", w.Code, err, w.Body.String())
	}
	bySubject := map[string]int{}
	for index, subject := range document.Subjects {
		bySubject[subject.NodeID] = index
	}
	a, b, c, d := document.Subjects[bySubject["NSH-410-AAAAAA"]], document.Subjects[bySubject["NSH-410-BBBBBB"]], document.Subjects[bySubject["NSH-410-CCCCCC"]], document.Subjects[bySubject["NSH-410-DDDDDD"]]

	// The gateway's decision is the same for all four (a new device).
	for _, subject := range document.Subjects {
		if subject.State != "OBSERVATION" {
			t.Fatalf("%s: gateway state %s", subject.NodeID, subject.State)
		}
	}
	if !a.Device.Known || !a.Device.Supported || !a.Device.MatchesGateway || !a.Device.Enforcing || a.Device.Generation != 3 || !a.Device.CheckedAt.Equal(checked) ||
		a.Evidence["modem_change"] != "observed" || a.Evidence["node_local"] != "observed" {
		t.Fatalf("an enforcing device: %+v %+v", a.Device, a.Evidence)
	}
	if !b.Device.Known || b.Device.Supported || b.Device.Enforcing || b.Device.Reason == "" ||
		b.Evidence["modem_change"] != "unsupported" || b.Evidence["node_local"] != "unsupported" {
		t.Fatalf("an older agent is unsupported, with the reason: %+v %+v", b.Device, b.Evidence)
	}
	// The gateway says OBSERVATION; the device says it holds TRUSTED, stale.
	// Both are shown; neither overwrites the other.
	if c.State != "OBSERVATION" || c.Device.State != "TRUSTED" || c.Device.MatchesGateway || !c.Device.Stale ||
		c.Evidence["modem_change"] != "unsupported" || c.Evidence["node_local"] != "observed" || c.Device.HardwareReason == "" {
		t.Fatalf("a device behind the gateway: %s %+v %+v", c.State, c.Device, c.Evidence)
	}
	if d.Device.Known || d.Device.Supported || d.Device.Enforcing || d.Device.Reason == "" ||
		d.Evidence["modem_change"] != "unknown" || d.Evidence["node_local"] != "unknown" {
		t.Fatalf("a device that cannot be asked is unknown, never a default: %+v %+v", d.Device, d.Evidence)
	}
	// The gateway-wide entries are "observed" only when true of every device.
	if document.Evidence["modem_change"] != "unsupported" || document.Evidence["node_local"] != "unsupported" ||
		document.Evidence["device_identity"] != "observed" || document.Evidence["location"] != "not_used" {
		t.Fatalf("gateway-wide evidence: %+v", document.Evidence)
	}
	for _, secret := range []string{"binding_hash", "sim_hash", "hardware_hash", "serial", "signature"} {
		if strings.Contains(w.Body.String(), `"`+secret) {
			t.Fatalf("the response exposes %s: %s", secret, w.Body.String())
		}
	}

	// Every device reporting both: the gateway-wide entries follow.
	for id := range fleet.devices {
		fleet.devices[id] = fleet.devices["NSH-410-AAAAAA"]
	}
	fleet.devices["NSH-410-DDDDDD"] = fleet.devices["NSH-410-AAAAAA"]
	w = httptest.NewRecorder()
	api.securityTrust(w, httptest.NewRequest(http.MethodGet, "/api/v1/security/trust", nil))
	document = trustDeviceDocument{}
	if err := json.Unmarshal(w.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Evidence["modem_change"] != "observed" || document.Evidence["node_local"] != "observed" {
		t.Fatalf("all devices report: %+v", document.Evidence)
	}
}
