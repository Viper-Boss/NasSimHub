package httpapi

import (
	"net/http"
	"strings"

	"github.com/human-agent65535/modemdeck/internal/license"
	"github.com/human-agent65535/modemdeck/internal/provision"
)

// Refusing to serve a data directory that belongs to another machine.
//
// The failure being prevented: a gateway sold preloaded is a gateway whose
// deployment directory gets copied, and everything that makes an installation
// itself lives in there -- the administrator account, the NKN identity, the
// message and contact database, the forwarding tokens. A copy boots, logs in
// with a password the new owner was never told, answers on the SAME NKN
// address as every other copy, and shows them whoever prepared the image's
// messages. Nothing about it looks wrong.
//
// So the gateway asks, once, whether this data directory was created here, and
// declines to serve until somebody resolves the answer. The evidence was
// already being collected and never read: internal/license writes a hardware
// fingerprint the first time it resolves the machine ID.
//
// THE REMEDY IS NOT AUTOMATIC. A wipe is unrecoverable, so the gateway states
// its finding and waits: it never resets on its own, however sure it is.

// provisionState resolves the clone decision once.
//
// Lazily and once, for the same reason the licence runtime is lazy: it reads
// sysfs and a file, and neither belongs in the boot path of a gateway that may
// never be cloned.
func (api *API) provisionState() provision.Decision {
	api.provisionOnce.Do(func() {
		runtime := api.licensing()
		if runtime == nil || runtime.store == nil {
			// No data directory yet -- fixture mode, or storage not ready.
			// Nothing to compare, so nothing to accuse.
			api.provisionDecision = provision.Decision{Status: provision.StatusReady}
			return
		}
		stored, found := runtime.store.StoredFingerprint()
		current, err := license.CollectFingerprint("")
		api.provisionDecision = provision.Detect(stored, found, current, err)
		if api.provisionDecision.Cloned() {
			api.logger.Error("this data directory was created on another machine",
				"stored_machine_id", api.provisionDecision.StoredMachineID,
				"machine_id", api.provisionDecision.CurrentMachineID,
				"diverging_sources", strings.Join(api.provisionDecision.DivergingSources, ","),
			)
		}
	})
	return api.provisionDecision
}

// cloned is the short question the rest of the API asks.
func (api *API) cloned() bool {
	return api.provisionState().Cloned()
}

// provisionResetRequest is the confirmation body.
type provisionResetRequest struct {
	// ConfirmMachineID must equal this machine's own ID, which the setup page
	// displays.
	//
	// A typed confirmation rather than a bare POST. This endpoint cannot
	// require a login -- the whole point is that the inherited credentials must
	// not work -- so without it, anything that can reach the gateway on the
	// local network could erase it with an empty request. Echoing a value that
	// is only on the screen in front of the operator makes it a deliberate act
	// rather than a stray one.
	ConfirmMachineID string `json:"confirm_machine_id"`
}

// provisionResource answers /api/v1/provision.
//
// GET reports the decision. It is readable without a session because the login
// page has to render the explanation, and it discloses only this machine's own
// identity and which hardware sources changed -- the machine ID is already
// disclosed by the NKN identity reply.
//
// POST /api/v1/provision/reset requests the wipe. It is accepted ONLY while
// the gateway has itself concluded the directory is a clone, so a working
// installation has no reachable path to this at all.
func (api *API) provisionResource(response http.ResponseWriter, request *http.Request) {
	decision := api.provisionState()

	switch request.URL.Path {
	case "/api/v1/provision":
		if request.Method != http.MethodGet {
			response.Header().Set("Allow", http.MethodGet)
			writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持读取", "")
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{
			"status":              string(decision.Status),
			"cloned":              decision.Cloned(),
			"reason":              decision.Reason,
			"machine_id":          decision.CurrentMachineID,
			"previous_machine_id": decision.StoredMachineID,
			"matching_sources":    decision.MatchingSources,
			"diverging_sources":   decision.DivergingSources,
		})
	case "/api/v1/provision/reset":
		if request.Method != http.MethodPost {
			response.Header().Set("Allow", http.MethodPost)
			writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST", "")
			return
		}
		if !decision.Cloned() {
			// Fail closed, and say which state it is in. A working gateway
			// must not have a reachable wipe endpoint, and an operator who
			// reaches this needs to know the gateway disagrees with them
			// rather than that the URL was wrong.
			writeError(response, http.StatusConflict, "provision_not_required",
				"本机数据目录没有被判定为来自其他设备，不会执行重置", "")
			return
		}
		var input provisionResetRequest
		if !decodeJSONBody(response, request, &input) {
			return
		}
		if strings.TrimSpace(input.ConfirmMachineID) == "" ||
			strings.TrimSpace(input.ConfirmMachineID) != decision.CurrentMachineID {
			writeError(response, http.StatusUnprocessableEntity, "confirmation_mismatch",
				"请输入界面上显示的本机标识以确认重置", "confirm_machine_id")
			return
		}
		directory, ok := api.homeSIMDataDirectory()
		if !ok {
			writeError(response, http.StatusServiceUnavailable, "storage_unavailable",
				"数据目录不可用", "")
			return
		}
		if err := provision.RequestReset(directory, provision.Request{
			Reason:           decision.Reason,
			StoredMachineID:  decision.StoredMachineID,
			CurrentMachineID: decision.CurrentMachineID,
		}); err != nil {
			api.writeInternalError(response, request, "request factory reset", err)
			return
		}
		api.logger.Warn("a factory reset was requested for a cloned data directory",
			"stored_machine_id", decision.StoredMachineID,
			"machine_id", decision.CurrentMachineID,
		)
		// The wipe itself happens on the next start, before anything opens the
		// database -- see cmd/modemdeck/main.go. Answering "restart required"
		// rather than "done" is the honest report: nothing has been removed
		// yet, and telling the operator otherwise would have them believe a
		// gateway they then leave running is already clean.
		writeJSON(response, http.StatusAccepted, map[string]any{
			"status":  "reset_pending",
			"message": "已记录重置请求。重启容器后将清空数据目录并重新进入初始化流程。",
		})
	default:
		writeError(response, http.StatusNotFound, "not_found", "未知路径", "")
	}
}
