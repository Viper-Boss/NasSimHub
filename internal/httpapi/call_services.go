package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
)

// Call forwarding and call waiting.
//
// The one thing that makes this different from every other settings endpoint
// in this package: there is NOTHING TO STORE. Forwarding and call waiting are
// held by the operator against the subscriber, so this file has no
// settings.json, no revision, and no optimistic-concurrency check. Adding one
// would be actively harmful -- a stored copy goes stale the moment somebody
// dials *21# from a handset with the same SIM, and the UI would then show a
// confident, wrong answer about where the customer's calls are going.
//
// So every read is a live round trip to the network, and it is slow. The UI is
// built to expect that rather than to hide it.

// CallServicesService is optional, like the DHT and WireGuard services: an
// older agent has no supplementary-service endpoints and the API must say so
// rather than invent a state.
type CallServicesService interface {
	CallServicesStatus(context.Context, string) (agentclient.CallServices, error)
	ApplyCallForwarding(
		context.Context,
		string,
		agentclient.SetCallForwarding,
	) (agentclient.CallServices, error)
	ApplyCallWaiting(
		context.Context,
		string,
		agentclient.SetCallWaiting,
	) (agentclient.CallServices, error)
}

type callServicesView struct {
	Services agentclient.CallServices `json:"call_services"`
	// Unavailable separates "the agent cannot do this" from "the network said
	// no". The UI shows the first as a capability notice and the second as a
	// retryable failure; collapsing them would tell an operator to check their
	// signal when the problem is their build.
	Unavailable bool   `json:"unavailable,omitempty"`
	Error       string `json:"error,omitempty"`
}

// callServicesHandler serves GET for a read and PUT for a change.
func (api *API) callServicesHandler(response http.ResponseWriter, request *http.Request, lineID string) {
	lineID = strings.TrimSpace(lineID)
	if lineID == "" {
		writeError(response, http.StatusBadRequest, "invalid_argument", "线路 ID 不能为空", "")
		return
	}
	if api.callServices == nil {
		// 200 with unavailable rather than 501: the panel needs to render and
		// explain itself, and a failed fetch gives it nothing to explain with.
		writeJSON(response, http.StatusOK, callServicesView{
			Unavailable: true,
			Error:       "呼叫转移和呼叫等待不可用：agent 未提供该能力",
		})
		return
	}

	switch request.Method {
	case http.MethodGet:
		services, err := api.callServices.CallServicesStatus(request.Context(), lineID)
		if err != nil {
			writeJSON(response, http.StatusOK, callServicesView{Error: err.Error()})
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, http.StatusOK, callServicesView{Services: services})
	case http.MethodPut:
		api.applyCallServiceChange(response, request, lineID)
	default:
		response.Header().Set("Allow", "GET, PUT")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 GET 和 PUT", "")
	}
}

// callServiceChange is one edit. Exactly one of the two sections is present,
// which is what keeps a single request from half-applying: two services with
// two separate network round trips cannot be made atomic, so they are not
// offered together.
type callServiceChange struct {
	Forwarding *agentclient.SetCallForwarding `json:"forwarding,omitempty"`
	Waiting    *agentclient.SetCallWaiting    `json:"waiting,omitempty"`
}

func (api *API) applyCallServiceChange(
	response http.ResponseWriter,
	request *http.Request,
	lineID string,
) {
	var change callServiceChange
	if err := json.NewDecoder(http.MaxBytesReader(response, request.Body, 4096)).Decode(&change); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_argument", "请求格式无效", "")
		return
	}
	switch {
	case change.Forwarding != nil && change.Waiting != nil:
		writeError(
			response, http.StatusBadRequest, "invalid_argument",
			"一次只能修改一项：转移和等待是两次独立的网络操作，无法一起回滚", "",
		)
		return
	case change.Forwarding != nil:
		services, err := api.callServices.ApplyCallForwarding(request.Context(), lineID, *change.Forwarding)
		if err != nil {
			writeJSON(response, http.StatusOK, callServicesView{Error: err.Error()})
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, http.StatusOK, callServicesView{Services: services})
	case change.Waiting != nil:
		services, err := api.callServices.ApplyCallWaiting(request.Context(), lineID, *change.Waiting)
		if err != nil {
			writeJSON(response, http.StatusOK, callServicesView{Error: err.Error()})
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, http.StatusOK, callServicesView{Services: services})
	default:
		writeError(response, http.StatusBadRequest, "invalid_argument", "请求里没有要修改的项", "")
	}
}
