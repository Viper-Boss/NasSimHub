package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/human-agent65535/modemdeck/internal/auth"
	"github.com/human-agent65535/modemdeck/internal/license"
	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/modemdeck/internal/trustengine"
)

// The security centre's trust and journal routes. All are administrator-only
// (adminOnlyAPIPath) and the writes carry the CSRF check every POST does.
//
//	GET  /api/v1/security/trust              every device's trust state, and the journal's health
//	POST /api/v1/security/trust/restrict     stop outgoing use of a device
//	POST /api/v1/security/trust/quarantine   revoke a device: only update and diagnosis remain
//	POST /api/v1/security/trust/restore      lift a restriction -> observation (never straight to trusted)
//	GET  /api/v1/security/journal            journal entries: ?after=<seq> oldest first, or ?order=desc[&before=<seq>] newest first
//	POST /api/v1/security/journal/verify     re-hash the whole journal now
//	POST /api/v1/security/journal/reseal     authorised recovery of a damaged journal
//
// Every answer is computed from the engine at the time of the request. When
// no engine is attached the routes say the feature is unavailable; they do not
// answer with an empty, healthy-looking document.

type trustProvider interface {
	Trust() *trustengine.Engine
}

func (api *API) trustEngine(w http.ResponseWriter) (*trustengine.Engine, bool) {
	provider, ok := api.nodeFleet.(trustProvider)
	if !ok || provider.Trust() == nil {
		writeError(w, http.StatusServiceUnavailable, "trust_unavailable", "Trust records are not available on this gateway", "")
		return nil, false
	}
	return provider.Trust(), true
}

type trustDocument struct {
	IssuerAdminAllowed bool `json:"issuer_admin_allowed"`
	// Enforcement is "enforce" or "monitor". In monitor mode the states below
	// are real but nothing is refused, and the UI must say so.
	Enforcement string                  `json:"enforcement"`
	Subjects    []trustSubjectDocument  `json:"subjects"`
	Journal     trustengine.ChainReport `json:"journal"`
	// Evidence states what the engine does and does not look at, so the page
	// can label a missing input as missing rather than as "fine". Two inputs
	// differ from device to device (modem_change, node_local); for those the
	// value here is "observed" only when it holds for every paired device, and
	// each subject carries its own.
	Evidence map[string]string `json:"evidence"`
}

// trustSubjectDocument is one device: the gateway's record of it (every field
// of trustengine.SubjectView, unchanged) and, added in r128, two things that
// are NOT the gateway's decision.
type trustSubjectDocument struct {
	trustengine.SubjectView
	// Device is what the device itself reports about holding and enforcing
	// the policy. "NAS 显示状态" is the fields above; "设备已执行" is this.
	Device nassimhubnode.DeviceEnforcementView `json:"device"`
	// Evidence is this device's value for the inputs that differ per device.
	Evidence map[string]string `json:"evidence"`
}

// Evidence values: observed, unsupported, not_used, unknown.
func deviceEvidence(device nassimhubnode.DeviceEnforcementView) map[string]string {
	evidence := map[string]string{"modem_change": "unknown", "node_local": "unknown"}
	if !device.Known {
		return evidence // not asked, or it did not answer: neither yes nor no
	}
	// A hardware identifier the device reports now. Without one a replaced
	// module cannot be detected, and nothing else is used in its place.
	evidence["modem_change"] = "unsupported"
	if device.HardwareReported {
		evidence["modem_change"] = "observed"
	}
	switch {
	case !device.Supported:
		evidence["node_local"] = "unsupported"
	case device.Installed:
		evidence["node_local"] = "observed"
	}
	return evidence
}

func trustEvidence(subjects []trustSubjectDocument) map[string]string {
	evidence := map[string]string{
		"device_identity": "observed",    // the pinned Ed25519 key, every poll
		"sim_change":      "observed",    // a keyed hash of the SIM identity, every poll
		"modem_change":    "unsupported", // per device: only one that reports a hardware identifier
		"otp_volume":      "observed",    // codes received in 24 h, from stored messages
		"location":        "not_used",    // never an input to a trust decision
		"node_local":      "unsupported", // per device: only one that holds the signed policy
	}
	for _, input := range []string{"modem_change", "node_local"} {
		all := len(subjects) > 0
		for _, subject := range subjects {
			all = all && subject.Evidence[input] == "observed"
		}
		if all {
			evidence[input] = "observed"
		}
	}
	return evidence
}

func (api *API) trustSubjects(engine *trustengine.Engine) []trustSubjectDocument {
	devices := map[string]nassimhubnode.DeviceEnforcementView{}
	if api.nodeFleet != nil {
		for _, view := range api.nodeFleet.Nodes() {
			devices[view.DeviceID] = view.Trust.Device
		}
	}
	views := engine.Subjects()
	subjects := make([]trustSubjectDocument, 0, len(views))
	for _, view := range views {
		device, paired := devices[view.SubjectID]
		if !paired {
			// A record outlives its pairing; there is no device to ask.
			device = nassimhubnode.DeviceEnforcementView{Reason: "the device is not paired with this gateway"}
		}
		subjects = append(subjects, trustSubjectDocument{SubjectView: view, Device: device, Evidence: deviceEvidence(device)})
	}
	return subjects
}

func (api *API) securityTrust(w http.ResponseWriter, r *http.Request) {
	engine, ok := api.trustEngine(w)
	if !ok {
		return
	}
	subjects := api.trustSubjects(engine)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, trustDocument{
		IssuerAdminAllowed: api.licensedFeature(r.Context(), license.FeatureTrustAdmin),
		Enforcement:        string(engine.Enforcement()), Subjects: subjects, Journal: engine.Chain(), Evidence: trustEvidence(subjects),
	})
}

type trustActionBody struct {
	DeviceID string `json:"device_id"`
	Reason   string `json:"reason"`
}

func actorOf(r *http.Request) string {
	if principal, ok := auth.PrincipalFromContext(r.Context()); ok && strings.TrimSpace(principal.Username) != "" {
		return principal.Username
	}
	return "local"
}

func writeTrustError(api *API, w http.ResponseWriter, r *http.Request, operation string, err error) {
	switch {
	case errors.Is(err, trustengine.ErrUnknownSubject):
		writeError(w, http.StatusNotFound, "trust_record_not_found", "This device has no trust record", "")
	case errors.Is(err, trustengine.ErrNotAllowed):
		writeError(w, http.StatusConflict, "trust_action_not_allowed", err.Error(), "")
	default:
		api.writeInternalError(w, r, operation, err)
	}
}

func (api *API) securityTrustAction(w http.ResponseWriter, r *http.Request, operation string,
	action func(context.Context, *trustengine.Engine, string, string, string) error) {
	engine, ok := api.trustEngine(w)
	if !ok {
		return
	}
	var body trustActionBody
	if !decodeJSONBodyWithLimit(w, r, &body, 2<<10) {
		return
	}
	deviceID, reason := strings.TrimSpace(body.DeviceID), strings.TrimSpace(body.Reason)
	if deviceID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "device_id is required", "device_id")
		return
	}
	// A reason is required: the journal exists to explain decisions, and an
	// administrator's is the one the engine cannot work out for itself.
	if reason == "" || len(reason) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_request", "reason is required (at most 200 characters)", "reason")
		return
	}
	if err := action(r.Context(), engine, deviceID, actorOf(r), reason); err != nil {
		writeTrustError(api, w, r, operation, err)
		return
	}
	api.securityTrust(w, r)
}

func (api *API) securityTrustRestrict(w http.ResponseWriter, r *http.Request) {
	api.securityTrustAction(w, r, "restrict device", func(ctx context.Context, e *trustengine.Engine, id, actor, reason string) error {
		return e.Restrict(ctx, id, actor, reason, false)
	})
}

func (api *API) securityTrustQuarantine(w http.ResponseWriter, r *http.Request) {
	api.securityTrustAction(w, r, "quarantine device", func(ctx context.Context, e *trustengine.Engine, id, actor, reason string) error {
		return e.Restrict(ctx, id, actor, reason, true)
	})
}

func (api *API) securityTrustRestore(w http.ResponseWriter, r *http.Request) {
	api.securityTrustAction(w, r, "restore device", func(ctx context.Context, e *trustengine.Engine, id, actor, reason string) error {
		return e.Restore(ctx, id, actor, reason)
	})
}

func (api *API) securityJournal(w http.ResponseWriter, r *http.Request) {
	engine, ok := api.trustEngine(w)
	if !ok {
		return
	}
	query := r.URL.Query()
	after, limit := int64(0), 100
	if raw := query.Get("after"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "invalid_request", "after must be a sequence number", "after")
			return
		}
		after = parsed
	}
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 500", "limit")
			return
		}
		limit = parsed
	}
	// ?before=<seq> (or ?order=desc alone) reads newest first, which is how a
	// viewer opens; ?after=<seq> reads forward, oldest first.
	var events []trustengine.Event
	var err error
	if raw := query.Get("before"); raw != "" || query.Get("order") == "desc" {
		before := int64(0)
		if raw != "" {
			parsed, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil || parsed < 1 {
				writeError(w, http.StatusBadRequest, "invalid_request", "before must be a sequence number", "before")
				return
			}
			before = parsed
		}
		events, err = engine.RecentEvents(r.Context(), before, limit, query.Get("node_id"))
	} else {
		events, err = engine.Events(r.Context(), after, limit, query.Get("node_id"))
	}
	if err != nil {
		api.writeInternalError(w, r, "read security journal", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Events  []trustengine.Event     `json:"events"`
		Journal trustengine.ChainReport `json:"journal"`
	}{events, engine.Chain()})
}

func (api *API) securityJournalVerify(w http.ResponseWriter, r *http.Request) {
	engine, ok := api.trustEngine(w)
	if !ok {
		return
	}
	report, err := engine.VerifyChain(r.Context())
	if err != nil {
		api.writeInternalError(w, r, "verify security journal", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Journal trustengine.ChainReport `json:"journal"`
	}{report})
}

func (api *API) securityJournalReseal(w http.ResponseWriter, r *http.Request) {
	engine, ok := api.trustEngine(w)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !decodeJSONBodyWithLimit(w, r, &body, 2<<10) {
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" || len(reason) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_request", "reason is required (at most 200 characters)", "reason")
		return
	}
	report, err := engine.Reseal(r.Context(), actorOf(r), reason)
	if err != nil {
		writeTrustError(api, w, r, "reseal security journal", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Journal trustengine.ChainReport `json:"journal"`
	}{report})
}

func (api *API) securityTrustApprove(w http.ResponseWriter, r *http.Request) {
	if !api.licensedFeature(r.Context(), license.FeatureTrustAdmin) {
		writeError(w, http.StatusForbidden, "issuer_authorization_required",
			"请在签发控制台签发并导入本 NAS 的「设备限制管理员解除」机器授权，然后重试。普通外呼或短信授权不包含此权限。", "")
		return
	}
	api.securityTrustAction(w, r, "approve device trust", func(ctx context.Context, e *trustengine.Engine, id, actor, reason string) error {
		return e.AdministratorTrust(ctx, id, actor, reason)
	})
}
