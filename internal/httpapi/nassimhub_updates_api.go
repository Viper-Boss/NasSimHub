package httpapi

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
)

// Agent updates for NasSimHub Nodes.
//
// Every route inherits the administrator and CSRF gates on /api/v1/nodes/.
// The gateway stores a signed manifest and its artifact, carries them to a
// device and asks the device to switch; the device verifies against its own
// release keys. Device status is read from the device on each request and is
// never served from memory.
//
//	GET  /api/v1/nodes/updates[?device_id=…]   stored releases, and one device's live status
//	POST /api/v1/nodes/updates/manifest        import a signed manifest (JSON, as signed)
//	POST /api/v1/nodes/updates/artifact?release_id=…   import its artifact (octet-stream)
//	POST /api/v1/nodes/updates/remove          forget a stored release
//	POST /api/v1/nodes/updates/push            offer + transfer to a device (no restart)
//	POST /api/v1/nodes/updates/apply           switch, wait for the device, confirm
//	POST /api/v1/nodes/updates/confirm         confirm by hand
//	POST /api/v1/nodes/updates/rollback        put the previous agent back

type nodeUpdateService interface {
	NodeReleases() nassimhubnode.NodeReleaseCatalog
	ImportNodeReleaseManifest([]byte) (nassimhubnode.NodeRelease, error)
	ImportNodeReleaseArtifact(string, io.Reader) (nassimhubnode.NodeRelease, error)
	RemoveNodeRelease(string) error
	NodeUpdateStatus(context.Context, string) (nassimhubnode.NodeUpdateView, error)
	PushStoredNodeUpdate(context.Context, string, string) (nassimhubnode.NodeUpdateView, error)
	ApplyNodeUpdate(context.Context, string, string, nassimhubnode.NodeUpdateApplyOptions) (nassimhubnode.NodeUpdateView, error)
	ConfirmNodeUpdate(context.Context, string, string) (nassimhubnode.NodeUpdateView, error)
	RollbackNodeUpdate(context.Context, string, string) (nassimhubnode.NodeUpdateView, error)
}

const (
	// nodeManifestLimit matches the protocol's bound on a signed manifest.
	nodeManifestLimit = 32 << 10
	// nodeArtifactLimit matches the protocol's bound on an agent artifact.
	nodeArtifactLimit = 32 << 20
	// nodeTransferTimeout bounds carrying an artifact to a device over a slow
	// link; nodeApplyTimeout the restart and confirmation.
	nodeTransferTimeout = 5 * time.Minute
	nodeApplyTimeout    = 3 * time.Minute
)

func (api *API) nodeUpdates(w http.ResponseWriter) (nodeUpdateService, bool) {
	service, ok := api.nodeFleet.(nodeUpdateService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "nodes_unavailable", "Node updates are unavailable", "")
		return nil, false
	}
	return service, true
}

func writeNodeUpdateError(api *API, w http.ResponseWriter, r *http.Request, operation string, err error) {
	type mapping struct {
		target  error
		status  int
		code    string
		message string
	}
	for _, candidate := range []mapping{
		{nassimhubnode.ErrNodeUpdatesUnsupported, http.StatusConflict, "updates_unsupported", "This device or gateway cannot handle agent updates."},
		{nassimhubnode.ErrNodeUpdateBusy, http.StatusConflict, "update_busy", "Another update operation is in progress for this device."},
		{nassimhubnode.ErrNodeReleaseUnknown, http.StatusNotFound, "release_not_found", "The release is not in the gateway's store."},
		{nassimhubnode.ErrNodeReleaseIncomplete, http.StatusConflict, "release_incomplete", "The release artifact has not been imported yet."},
		{nassimhubnode.ErrNodeUpdateNotConfirmed, http.StatusConflict, "update_not_confirmed", "The device did not come back on the new version in time. It returns to its previous agent on its own."},
	} {
		if errors.Is(err, candidate.target) {
			writeError(w, candidate.status, candidate.code, candidate.message, "")
			return
		}
	}
	if errors.Is(err, nassimhubnode.ErrNodeReleaseRejected) {
		// The reason is the device's or the gateway's own wording about a
		// manifest or artifact. It names release ids, versions and key ids -
		// never keys, and nothing about the line.
		writeError(w, http.StatusUnprocessableEntity, "release_rejected", err.Error(), "")
		return
	}
	writeNodeError(api, w, r, operation, err)
}

func (api *API) nassimhubNodeUpdates(w http.ResponseWriter, r *http.Request) {
	service, ok := api.nodeUpdates(w)
	if !ok {
		return
	}
	response := struct {
		nassimhubnode.NodeReleaseCatalog
		Device *nassimhubnode.NodeUpdateView `json:"device,omitempty"`
	}{NodeReleaseCatalog: service.NodeReleases()}
	if deviceID := strings.TrimSpace(r.URL.Query().Get("device_id")); deviceID != "" {
		ctx, cancel := context.WithTimeout(r.Context(), nodeOperationTimeout)
		defer cancel()
		view, err := service.NodeUpdateStatus(ctx, deviceID)
		if err != nil {
			writeNodeUpdateError(api, w, r, "read NasSimHub node update status", err)
			return
		}
		response.Device = &view
	}
	// Live device state must not be reused by a browser or a proxy.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

func (api *API) nassimhubNodeUpdateManifest(w http.ResponseWriter, r *http.Request) {
	service, ok := api.nodeUpdates(w)
	if !ok {
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json", "")
		return
	}
	// The body is stored exactly as received: the signature covers these
	// bytes, so it is not decoded and re-encoded here.
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, nodeManifestLimit))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "manifest_too_large", "The signed manifest is too large", "")
		return
	}
	release, err := service.ImportNodeReleaseManifest(raw)
	if err != nil {
		writeNodeUpdateError(api, w, r, "import NasSimHub node release manifest", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Release nassimhubnode.NodeRelease `json:"release"`
	}{release})
}

func (api *API) nassimhubNodeUpdateArtifact(w http.ResponseWriter, r *http.Request) {
	service, ok := api.nodeUpdates(w)
	if !ok {
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/octet-stream" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/octet-stream", "")
		return
	}
	releaseID := strings.TrimSpace(r.URL.Query().Get("release_id"))
	if releaseID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "release_id is required", "release_id")
		return
	}
	if r.ContentLength > nodeArtifactLimit {
		writeError(w, http.StatusRequestEntityTooLarge, "artifact_too_large", "The artifact is too large to be an agent", "")
		return
	}
	release, err := service.ImportNodeReleaseArtifact(releaseID, http.MaxBytesReader(w, r.Body, nodeArtifactLimit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "artifact_too_large", "The artifact is too large to be an agent", "")
			return
		}
		writeNodeUpdateError(api, w, r, "import NasSimHub node release artifact", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Release nassimhubnode.NodeRelease `json:"release"`
	}{release})
}

type nodeReleaseBody struct {
	ReleaseID string `json:"release_id"`
}

func (api *API) nassimhubNodeUpdateRemove(w http.ResponseWriter, r *http.Request) {
	service, ok := api.nodeUpdates(w)
	if !ok {
		return
	}
	var body nodeReleaseBody
	if !decodeJSONBodyWithLimit(w, r, &body, 1<<10) {
		return
	}
	if strings.TrimSpace(body.ReleaseID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "release_id is required", "release_id")
		return
	}
	if err := service.RemoveNodeRelease(strings.TrimSpace(body.ReleaseID)); err != nil {
		writeNodeUpdateError(api, w, r, "remove NasSimHub node release", err)
		return
	}
	writeJSON(w, http.StatusOK, service.NodeReleases())
}

type nodeUpdateBody struct {
	DeviceID  string `json:"device_id"`
	ReleaseID string `json:"release_id"`
}

func (api *API) nodeUpdateAction(
	w http.ResponseWriter, r *http.Request, operation string, timeout time.Duration,
	action func(nodeUpdateService, context.Context, string, string) (nassimhubnode.NodeUpdateView, error),
) {
	service, ok := api.nodeUpdates(w)
	if !ok {
		return
	}
	var body nodeUpdateBody
	if !decodeJSONBodyWithLimit(w, r, &body, 1<<10) {
		return
	}
	deviceID, releaseID := strings.TrimSpace(body.DeviceID), strings.TrimSpace(body.ReleaseID)
	if deviceID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "device_id is required", "device_id")
		return
	}
	if releaseID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "release_id is required", "release_id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	view, err := action(service, ctx, deviceID, releaseID)
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		if errors.Is(err, nassimhubnode.ErrNodeUpdateNotConfirmed) && view.Status != nil {
			// Not confirmed is an outcome with a state worth showing: what
			// the device last said before the wait ran out.
			writeJSON(w, http.StatusConflict, struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
				Device nassimhubnode.NodeUpdateView `json:"device"`
			}{Error: struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}{"update_not_confirmed", "The device did not come back on the new version in time. It returns to its previous agent on its own."}, Device: view})
			return
		}
		writeNodeUpdateError(api, w, r, operation, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Device nassimhubnode.NodeUpdateView `json:"device"`
	}{view})
}

func (api *API) nassimhubNodeUpdatePush(w http.ResponseWriter, r *http.Request) {
	api.nodeUpdateAction(w, r, "push NasSimHub node update", nodeTransferTimeout,
		func(s nodeUpdateService, ctx context.Context, device, release string) (nassimhubnode.NodeUpdateView, error) {
			return s.PushStoredNodeUpdate(ctx, device, release)
		})
}

func (api *API) nassimhubNodeUpdateApply(w http.ResponseWriter, r *http.Request) {
	api.nodeUpdateAction(w, r, "apply NasSimHub node update", nodeApplyTimeout,
		func(s nodeUpdateService, ctx context.Context, device, release string) (nassimhubnode.NodeUpdateView, error) {
			return s.ApplyNodeUpdate(ctx, device, release, nassimhubnode.NodeUpdateApplyOptions{})
		})
}

func (api *API) nassimhubNodeUpdateConfirm(w http.ResponseWriter, r *http.Request) {
	api.nodeUpdateAction(w, r, "confirm NasSimHub node update", nodeOperationTimeout,
		func(s nodeUpdateService, ctx context.Context, device, release string) (nassimhubnode.NodeUpdateView, error) {
			return s.ConfirmNodeUpdate(ctx, device, release)
		})
}

func (api *API) nassimhubNodeUpdateRollback(w http.ResponseWriter, r *http.Request) {
	api.nodeUpdateAction(w, r, "roll back NasSimHub node update", nodeOperationTimeout,
		func(s nodeUpdateService, ctx context.Context, device, release string) (nassimhubnode.NodeUpdateView, error) {
			return s.RollbackNodeUpdate(ctx, device, release)
		})
}
