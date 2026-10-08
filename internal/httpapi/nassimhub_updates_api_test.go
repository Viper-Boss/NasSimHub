package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/nassimhub-node/proto"
)

type updateFleet struct {
	fakeNodeFleet
	calls     []string
	manifests [][]byte
	artifact  []byte
	statusN   int
	err       error
	view      nassimhubnode.NodeUpdateView
}

func (f *updateFleet) NodeReleases() nassimhubnode.NodeReleaseCatalog {
	return nassimhubnode.NodeReleaseCatalog{Available: true, Releases: []nassimhubnode.NodeRelease{
		{ReleaseID: "r-1.1.0", Version: "1.1.0", Gateway: nassimhubnode.GatewayUnverified}}}
}
func (f *updateFleet) ImportNodeReleaseManifest(raw []byte) (nassimhubnode.NodeRelease, error) {
	f.manifests = append(f.manifests, raw)
	return nassimhubnode.NodeRelease{ReleaseID: "r-1.1.0"}, f.err
}
func (f *updateFleet) ImportNodeReleaseArtifact(id string, body io.Reader) (nassimhubnode.NodeRelease, error) {
	content, err := io.ReadAll(body)
	if err != nil {
		return nassimhubnode.NodeRelease{}, err
	}
	f.artifact = content
	f.calls = append(f.calls, "artifact:"+id)
	return nassimhubnode.NodeRelease{ReleaseID: id, ArtifactReady: true}, f.err
}
func (f *updateFleet) RemoveNodeRelease(id string) error {
	f.calls = append(f.calls, "remove:"+id)
	return f.err
}
func (f *updateFleet) NodeUpdateStatus(_ context.Context, id string) (nassimhubnode.NodeUpdateView, error) {
	f.statusN++
	status := proto.OTADeviceStatus{}
	status.CurrentVersion = fmt.Sprintf("1.0.%d", f.statusN)
	return nassimhubnode.NodeUpdateView{DeviceID: id, Reachable: true, Supported: true, Status: &status}, f.err
}
func (f *updateFleet) action(name, device, release string) (nassimhubnode.NodeUpdateView, error) {
	f.calls = append(f.calls, name+":"+device+":"+release)
	return f.view, f.err
}
func (f *updateFleet) PushStoredNodeUpdate(_ context.Context, d, r string) (nassimhubnode.NodeUpdateView, error) {
	return f.action("push", d, r)
}
func (f *updateFleet) ApplyNodeUpdate(_ context.Context, d, r string, _ nassimhubnode.NodeUpdateApplyOptions) (nassimhubnode.NodeUpdateView, error) {
	return f.action("apply", d, r)
}
func (f *updateFleet) ConfirmNodeUpdate(_ context.Context, d, r string) (nassimhubnode.NodeUpdateView, error) {
	return f.action("confirm", d, r)
}
func (f *updateFleet) RollbackNodeUpdate(_ context.Context, d, r string) (nassimhubnode.NodeUpdateView, error) {
	return f.action("rollback", d, r)
}

func TestNodeUpdateRoutesAreAdminOnly(t *testing.T) {
	for _, path := range []string{"/api/v1/nodes/updates", "/api/v1/nodes/updates/manifest", "/api/v1/nodes/updates/artifact",
		"/api/v1/nodes/updates/remove", "/api/v1/nodes/updates/push", "/api/v1/nodes/updates/apply",
		"/api/v1/nodes/updates/confirm", "/api/v1/nodes/updates/rollback"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			if !adminOnlyAPIPath(path, method) {
				t.Fatalf("%s %s must be administrator-only", method, path)
			}
		}
	}
}

func TestNodeUpdateStatusIsReadFromTheDeviceEveryTimeAndNeverCacheable(t *testing.T) {
	fleet := &updateFleet{}
	api := &API{nodeFleet: fleet}
	seen := map[string]bool{}
	for range 3 {
		w := httptest.NewRecorder()
		api.nassimhubNodeUpdates(w, httptest.NewRequest(http.MethodGet, "/api/v1/nodes/updates?device_id=NSH-1", nil))
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status: %d cache=%q", w.Code, w.Header().Get("Cache-Control"))
		}
		var body struct {
			Device struct {
				Status struct {
					CurrentVersion string `json:"current_version"`
				} `json:"status"`
			} `json:"device"`
			GatewayVerifies bool `json:"gateway_verifies"`
			Releases        []struct {
				Gateway string `json:"gateway_verification"`
			} `json:"releases"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.GatewayVerifies || len(body.Releases) != 1 || body.Releases[0].Gateway != "unverified" {
			t.Fatalf("a gateway without keys must list releases as unverified: %s", w.Body.String())
		}
		seen[body.Device.Status.CurrentVersion] = true
	}
	if fleet.statusN != 3 || len(seen) != 3 {
		t.Fatalf("each request must ask the device again: asked %d times, saw %v", fleet.statusN, seen)
	}
	// Without a device id nothing is asked of any device.
	w := httptest.NewRecorder()
	api.nassimhubNodeUpdates(w, httptest.NewRequest(http.MethodGet, "/api/v1/nodes/updates", nil))
	if w.Code != http.StatusOK || fleet.statusN != 3 || strings.Contains(w.Body.String(), `"device"`) {
		t.Fatalf("catalog only: %d %s", w.Code, w.Body.String())
	}
}

func TestNodeUpdateManifestIsStoredByteForByte(t *testing.T) {
	fleet := &updateFleet{}
	api := &API{nodeFleet: fleet}
	// Deliberately odd spacing and key order: a re-encode would change it.
	raw := "{ \"signature\":\"c2ln\" ,\"manifest\": {\"b\":1, \"a\":2},\n\"key_id\":\"k\"}"
	w := httptest.NewRecorder()
	api.nassimhubNodeUpdateManifest(w, jsonRequest(http.MethodPost, "/api/v1/nodes/updates/manifest", raw))
	if w.Code != http.StatusOK || len(fleet.manifests) != 1 || string(fleet.manifests[0]) != raw {
		t.Fatalf("manifest import: %d %q", w.Code, fleet.manifests)
	}
	w = httptest.NewRecorder()
	api.nassimhubNodeUpdateManifest(w, jsonRequest(http.MethodPost, "/api/v1/nodes/updates/manifest", strings.Repeat("x", nodeManifestLimit+1)))
	if w.Code != http.StatusRequestEntityTooLarge || len(fleet.manifests) != 1 {
		t.Fatalf("an oversized manifest must be refused before it is stored: %d", w.Code)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/nodes/updates/manifest", strings.NewReader(raw))
	request.Header.Set("Content-Type", "text/plain")
	w = httptest.NewRecorder()
	api.nassimhubNodeUpdateManifest(w, request)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("content type: %d", w.Code)
	}
	fleet.err = fmt.Errorf("%w: signature does not verify against key \"k\"", nassimhubnode.ErrNodeReleaseRejected)
	w = httptest.NewRecorder()
	api.nassimhubNodeUpdateManifest(w, jsonRequest(http.MethodPost, "/api/v1/nodes/updates/manifest", raw))
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "release_rejected") || !strings.Contains(w.Body.String(), "does not verify") {
		t.Fatalf("a refused manifest must say why: %d %s", w.Code, w.Body.String())
	}
}

func TestNodeUpdateArtifactIsBounded(t *testing.T) {
	fleet := &updateFleet{}
	api := &API{nodeFleet: fleet}
	post := func(target string, body io.Reader, length int64, contentType string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, target, body)
		request.Header.Set("Content-Type", contentType)
		if length >= 0 {
			request.ContentLength = length
		}
		w := httptest.NewRecorder()
		api.nassimhubNodeUpdateArtifact(w, request)
		return w
	}
	content := bytes.Repeat([]byte{7}, 4096)
	if w := post("/api/v1/nodes/updates/artifact?release_id=r-1.1.0", bytes.NewReader(content), int64(len(content)), "application/octet-stream"); w.Code != http.StatusOK || !bytes.Equal(fleet.artifact, content) {
		t.Fatalf("artifact import: %d", w.Code)
	}
	if w := post("/api/v1/nodes/updates/artifact", bytes.NewReader(content), int64(len(content)), "application/octet-stream"); w.Code != http.StatusBadRequest {
		t.Fatalf("missing release id: %d", w.Code)
	}
	if w := post("/api/v1/nodes/updates/artifact?release_id=r", bytes.NewReader(content), int64(len(content)), "application/json"); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("content type: %d", w.Code)
	}
	calls := len(fleet.calls)
	if w := post("/api/v1/nodes/updates/artifact?release_id=r", bytes.NewReader(nil), nodeArtifactLimit+1, "application/octet-stream"); w.Code != http.StatusRequestEntityTooLarge || len(fleet.calls) != calls {
		t.Fatalf("a declared oversize must be refused before reading: %d", w.Code)
	}
	// An undeclared oversize is cut off while reading.
	big := io.LimitReader(zeroReader{}, nodeArtifactLimit+2)
	if w := post("/api/v1/nodes/updates/artifact?release_id=r", big, -1, "application/octet-stream"); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("an undeclared oversize must be refused: %d", w.Code)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestNodeUpdateActionsNameBothTheDeviceAndTheRelease(t *testing.T) {
	fleet := &updateFleet{}
	api := &API{nodeFleet: fleet}
	handlers := map[string]http.HandlerFunc{
		"push": api.nassimhubNodeUpdatePush, "apply": api.nassimhubNodeUpdateApply,
		"confirm": api.nassimhubNodeUpdateConfirm, "rollback": api.nassimhubNodeUpdateRollback,
	}
	for _, name := range []string{"push", "apply", "confirm", "rollback"} {
		handler := handlers[name]
		w := httptest.NewRecorder()
		handler(w, jsonRequest(http.MethodPost, "/api/v1/nodes/updates/"+name, `{"device_id":"NSH-1","release_id":"r-1.1.0"}`))
		if w.Code != http.StatusOK || fleet.calls[len(fleet.calls)-1] != name+":NSH-1:r-1.1.0" {
			t.Fatalf("%s: %d %v", name, w.Code, fleet.calls)
		}
		calls := len(fleet.calls)
		for _, bad := range []string{`{"device_id":"NSH-1"}`, `{"release_id":"r-1.1.0"}`, `{}`,
			`{"device_id":"NSH-1","release_id":"r","force":true}`} {
			w = httptest.NewRecorder()
			handler(w, jsonRequest(http.MethodPost, "/api/v1/nodes/updates/"+name, bad))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s accepted %s: %d", name, bad, w.Code)
			}
		}
		if len(fleet.calls) != calls {
			t.Fatalf("%s: a refused request reached the fleet", name)
		}
	}

	for target, want := range map[error]string{
		nassimhubnode.ErrNodeUpdateBusy:         "update_busy",
		nassimhubnode.ErrNodeReleaseUnknown:     "release_not_found",
		nassimhubnode.ErrNodeReleaseIncomplete:  "release_incomplete",
		nassimhubnode.ErrNodeUpdatesUnsupported: "updates_unsupported",
		nassimhubnode.ErrNodeUnavailable:        "node_unreachable",
		nassimhubnode.ErrUnknownNode:            "node_not_found",
	} {
		fleet.err = target
		w := httptest.NewRecorder()
		api.nassimhubNodeUpdatePush(w, jsonRequest(http.MethodPost, "/api/v1/nodes/updates/push", `{"device_id":"NSH-1","release_id":"r-1.1.0"}`))
		if w.Code < 400 || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("%v: %d %s", target, w.Code, w.Body.String())
		}
	}

	// An unconfirmed update reports the last thing the device said.
	status := proto.OTADeviceStatus{}
	status.State = proto.OTAPendingConfirm
	fleet.err, fleet.view = nassimhubnode.ErrNodeUpdateNotConfirmed, nassimhubnode.NodeUpdateView{DeviceID: "NSH-1", Reachable: true, Status: &status}
	w := httptest.NewRecorder()
	api.nassimhubNodeUpdateApply(w, jsonRequest(http.MethodPost, "/api/v1/nodes/updates/apply", `{"device_id":"NSH-1","release_id":"r-1.1.0"}`))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "update_not_confirmed") || !strings.Contains(w.Body.String(), "PENDING_CONFIRM") {
		t.Fatalf("unconfirmed apply: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("update results must not be cacheable")
	}
}

func TestNodeUpdateRoutesWithoutAFleetAreUnavailable(t *testing.T) {
	api := &API{nodeFleet: &fakeNodeFleet{}}
	w := httptest.NewRecorder()
	api.nassimhubNodeUpdates(w, httptest.NewRequest(http.MethodGet, "/api/v1/nodes/updates", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no update service: %d", w.Code)
	}
}
