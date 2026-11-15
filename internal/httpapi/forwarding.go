// SPDX-License-Identifier: AGPL-3.0-only
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/auth"
	"github.com/human-agent65535/modemdeck/internal/forwarding"
	"github.com/human-agent65535/modemdeck/internal/smsclass"
	"github.com/human-agent65535/modemdeck/internal/store"
)

const forwardingAPI = "/api/v1/settings/forwarding"

func forwardingReplaced(w http.ResponseWriter) {
	writeError(w, http.StatusGone, "forwarding_replaced", "Use /api/v1/settings/forwarding; explicitly migrate and review disabled legacy channels", "")
}
func (api *API) forwardingPrincipal(ctx context.Context, owner string) (auth.Principal, error) {
	repo, ok := api.repository.(userRepository)
	if !ok {
		return auth.Principal{}, forwarding.ErrDenied
	}
	users, err := repo.Users(ctx)
	if err != nil {
		// A failed lookup cannot establish revocation. Expose a fixed retryable
		// class rather than leaking the repository error or cancelling work.
		return auth.Principal{}, forwarding.ErrUnavailable
	}
	for _, user := range users {
		if user.ID == owner && user.Enabled && user.Role == auth.RoleAdmin {
			return auth.Principal{UserID: user.ID, Username: user.Username, Role: user.Role, AllowedLineIDs: append([]string(nil), user.LineIDs...)}, nil
		}
	}
	return auth.Principal{}, forwarding.ErrDenied
}
func (api *API) authorizeForwarding(ctx context.Context, owner string, event forwarding.Event, ch forwarding.Channel) error {
	principal, err := api.forwardingPrincipal(ctx, owner)
	if err != nil {
		return err
	}
	if event.Type != "test" && !principal.CanAccessLine(event.LineID) {
		return forwarding.ErrDenied
	}
	if ch.Type == "sms" && !principal.CanAccessLine(ch.Params["line_id"]) {
		return forwarding.ErrDenied
	}
	if event.Type == store.NotificationIncomingSMS && smsclass.Classify(event.Body) == smsclass.CategoryOTP {
		if api.forwardingOTP == nil {
			if strings.HasPrefix(event.EndpointID, store.NasSimHubNodeEndpointPrefix) || strings.HasPrefix(event.EndpointMessageID, "nsh-in:") {
				return forwarding.ErrDenied
			}
		} else if !api.forwardingOTP(ctx, store.Message{LineID: event.LineID, Content: event.Body, EndpointLineID: event.EndpointID, EndpointMessageID: event.EndpointMessageID}) {
			return forwarding.ErrDenied
		}
	}
	return nil
}
func (api *API) forwardingDispatcher() *forwarding.Dispatcher {
	return &forwarding.Dispatcher{Secrets: api.settingsSecrets, Transport: func(ctx context.Context) http.RoundTripper {
		return api.homeSIMOutboundTransport((&http.Request{}).WithContext(ctx))
	}, SMS: func(ctx context.Context, line, to, body, id string) error {
		if api.communications == nil {
			return forwarding.ErrUnavailable
		}
		principal, err := api.forwardingPrincipal(ctx, forwarding.SenderOwner(ctx))
		if err != nil {
			return err
		}
		if !principal.CanAccessLine(line) {
			return forwarding.ErrDenied
		}
		payload, _ := json.Marshal(map[string]string{"request_id": id, "line_id": line, "to": to, "content": body})
		request := httptest.NewRequestWithContext(auth.ContextWithPrincipal(ctx, principal), http.MethodPost, "/api/v1/messages", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		// Reuse the original business handler, including the anti-fraud guard,
		// stable request-id replay and current line access. This is an in-process
		// call after the queue's own authority checks, not a public CSRF bypass.
		api.sendMessage(result, request)
		if result.Code == http.StatusCreated {
			return nil
		}
		if result.Code >= 400 && result.Code < 500 {
			return forwarding.ErrDenied
		}
		return forwarding.ErrDelivery

	}}
}
func (api *API) StartForwardingWorker(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if api.forwardingStore == nil {
		close(done)
		return done
	}
	engine := &forwarding.Engine{Store: api.forwardingStore, Sender: api.forwardingDispatcher(), Authorize: api.authorizeForwarding, Report: func(code string) { api.logger.Warn("forwarding queue unavailable", "error_class", code) }}
	go func() { defer close(done); _ = engine.Run(ctx) }()
	return done
}
func (api *API) forwardingResource(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if api.forwardingStore == nil {
		writeError(w, 503, "forwarding_unavailable", "Forwarding storage is unavailable", "")
		return
	}
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || !principal.IsAdmin() {
		writeError(w, 403, "admin_required", "Administrator access is required", "")
		return
	}
	cfg, err := api.forwardingStore.Config(r.Context())
	if err != nil {
		api.forwardingFailure(w, err)
		return
	}
	switch r.URL.Path {
	case forwardingAPI:
		if r.Method == http.MethodGet {
			writeJSON(w, 200, map[string]any{"config": cfg.Public(), "catalog": forwarding.Catalog, "unsupported": []string{"android_url_scheme", "android_app_broadcast", "android_notification_access", "nkn_sms"}})
			return
		}
		if r.Method != http.MethodPut {
			forwardingMethod(w, "GET, PUT")
			return
		}
		var next forwarding.Config
		if !decodeJSONBody(w, r, &next) {
			return
		}
		next.OwnerID = principal.UserID
		for _, rule := range next.Rules {
			for _, id := range rule.LineIDs {
				if !principal.CanAccessLine(id) {
					api.forwardingFailure(w, forwarding.ErrDenied)
					return
				}
			}
		}
		for _, ch := range next.Channels {
			if ch.Type == "sms" && !principal.CanAccessLine(ch.Params["line_id"]) {
				api.forwardingFailure(w, forwarding.ErrDenied)
				return
			}
		}
		next, err = forwarding.SealConfig(next, cfg, api.settingsSecrets)
		if err == nil {
			next, err = api.forwardingStore.Save(r.Context(), next)
		}
		if err != nil {
			api.forwardingFailure(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"config": next.Public()})
	case forwardingAPI + "/deliveries":
		if r.Method != http.MethodGet {
			forwardingMethod(w, "GET")
			return
		}
		before := int64(0)
		if raw := r.URL.Query().Get("before"); raw != "" {
			before, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || before <= 0 {
				api.forwardingFailure(w, forwarding.ErrInvalid)
				return
			}
		}
		tasks, err := api.forwardingStore.History(r.Context(), before)
		if err != nil {
			api.forwardingFailure(w, err)
			return
		}
		filtered := []forwarding.Task{}
		next := int64(0)
		for _, task := range tasks {
			next = task.ID
			if principal.CanAccessLine(task.LineID) {
				filtered = append(filtered, task)
			}
		}
		writeJSON(w, 200, map[string]any{"deliveries": filtered, "next_before": next})
	case forwardingAPI + "/retry":
		if r.Method != http.MethodPost {
			forwardingMethod(w, "POST")
			return
		}
		var body struct {
			ID int64 `json:"id"`
		}
		if !decodeJSONBody(w, r, &body) {
			return
		}
		task, err := api.forwardingStore.Task(r.Context(), body.ID)
		if err != nil || !principal.CanAccessLine(task.LineID) {
			api.forwardingFailure(w, forwarding.ErrDenied)
			return
		}
		if err = api.forwardingStore.Retry(r.Context(), body.ID, time.Now()); err != nil {
			api.forwardingFailure(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"queued": true})
	case forwardingAPI + "/preview":
		if r.Method != http.MethodPost {
			forwardingMethod(w, "POST")
			return
		}
		var body struct {
			Config forwarding.Config `json:"config"`
			RuleID string            `json:"rule_id"`
			Event  forwarding.Event  `json:"event"`
		}
		if !decodeJSONBody(w, r, &body) {
			return
		}
		if body.Config.Validate() != nil {
			api.forwardingFailure(w, forwarding.ErrInvalid)
			return
		}
		for _, rule := range body.Config.Rules {
			if rule.ID == body.RuleID {
				rendered, err := body.Config.Render(rule, body.Event)
				if err != nil {
					api.forwardingFailure(w, err)
					return
				}
				writeJSON(w, 200, map[string]any{"matched": rule.Matches(body.Event), "rendered": rendered, "sends": false})
				return
			}
		}
		api.forwardingFailure(w, forwarding.ErrInvalid)
	case forwardingAPI + "/test":
		if r.Method != http.MethodPost {
			forwardingMethod(w, "POST")
			return
		}
		var body struct {
			ChannelID string `json:"channel_id"`
		}
		if !decodeJSONBody(w, r, &body) {
			return
		}
		for _, ch := range cfg.Channels {
			if ch.ID != body.ChannelID {
				continue
			}
			event := forwarding.Event{Type: "test"}
			if err = api.authorizeForwarding(r.Context(), principal.UserID, event, ch); err != nil {
				api.forwardingFailure(w, err)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			defer cancel()
			ctx = forwarding.ContextWithSenderOwner(ctx, principal.UserID)
			err = api.forwardingDispatcher().Send(ctx, ch, forwarding.Rendered{Title: "NasSimHub test", Body: "Synthetic forwarding channel test; no stored SMS content."}, "forwarding_test_"+strconv.FormatInt(time.Now().UnixNano(), 10))
			if err != nil {
				writeError(w, 502, "delivery_failed", "Channel test failed; check channel settings and server credentials", "")
				return
			}
			writeJSON(w, 200, map[string]bool{"accepted": true})
			return
		}
		api.forwardingFailure(w, forwarding.ErrInvalid)
	case forwardingAPI + "/migrate":
		if r.Method != http.MethodPost {
			forwardingMethod(w, "POST")
			return
		}
		api.migrateForwarding(w, r, cfg, principal)
	default:
		writeError(w, 404, "not_found", "Forwarding resource not found", "")
	}
}
func forwardingMethod(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	writeError(w, 405, "method_not_allowed", "Method is not supported", "")
}
func (api *API) forwardingFailure(w http.ResponseWriter, err error) {
	status, code := 503, "forwarding_unavailable"
	if errors.Is(err, forwarding.ErrInvalid) {
		status, code = 400, "invalid_forwarding_configuration"
	}
	if errors.Is(err, forwarding.ErrConflict) {
		status, code = 409, "revision_conflict"
	}
	if errors.Is(err, forwarding.ErrDenied) {
		status, code = 403, "forwarding_denied"
	}
	writeError(w, status, code, "Forwarding request refused; reload or check the configuration", "")
}
