package ota

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/human-agent65535/modemdeck/internal/updatecheck"
)

const maximumRequestBytes = 16 << 10

type Handler struct {
	controller  *Controller
	token       string
	downloader  *Downloader
	preferences PreferenceStore
	running     RunningImagesReporter
	release     ReleaseRelay
}

// ReleaseRelay is the release channel as the peer networks see it: the api
// process speaks NKN and asks the DHT, and hands whatever the other gateways
// answered to the channel here, which verifies it like any mirror's. The
// other way round, the api fetches the signed document to share.
type ReleaseRelay interface {
	Offer(kind string, signed []byte, peers int) (updatecheck.Source, error)
	Signed() []byte
}

// ReleaseOffer is the body of POST /v1/release/offer and of
// GET /v1/release/signed. The document travels as base64 so it arrives as
// the exact bytes the other gateway sent.
type ReleaseOffer struct {
	Kind         string `json:"kind,omitempty"`
	SignedBase64 string `json:"signed_base64"`
	Peers        int    `json:"peers,omitempty"`
}

// maximumOfferBytes fits a manifest at its size limit, base64-encoded, with
// room for the envelope.
const maximumOfferBytes = 96 << 10

// WithRelease enables the peer-network routes.
func (handler *Handler) WithRelease(relay ReleaseRelay) *Handler {
	handler.release = relay
	return handler
}

// RunningImagesReporter answers what every component is running.
type RunningImagesReporter interface {
	RunningImages(context.Context) ([]updatecheck.RunningImage, error)
}

// WithDownloads enables the download and preference routes.
func (handler *Handler) WithDownloads(downloader *Downloader, preferences PreferenceStore) *Handler {
	handler.downloader = downloader
	handler.preferences = preferences
	return handler
}

// WithRunning enables the running-images route.
func (handler *Handler) WithRunning(reporter RunningImagesReporter) *Handler {
	handler.running = reporter
	return handler
}

func NewHandler(controller *Controller, token string) (*Handler, error) {
	if controller == nil || strings.TrimSpace(token) == "" {
		return nil, errors.New("updater controller and authentication token are required")
	}
	return &Handler{controller: controller, token: strings.TrimSpace(token)}, nil
}

func (handler *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if request.URL.Path == "/health" {
		if request.Method != http.MethodGet {
			methodNotAllowed(response, http.MethodGet)
			return
		}
		writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if !handler.authorized(request) {
		writeError(response, http.StatusUnauthorized, "unauthorized")
		return
	}
	switch request.URL.Path {
	case "/v1/updates/check":
		if request.Method != http.MethodGet {
			methodNotAllowed(response, http.MethodGet)
			return
		}
		ctx := request.Context()
		if request.URL.Query().Get("refresh") == "1" {
			ctx = updatecheck.WithRefresh(ctx)
		}
		writeJSON(response, http.StatusOK, handler.controller.Check(ctx))
	case "/v1/updates/status":
		if request.Method != http.MethodGet {
			methodNotAllowed(response, http.MethodGet)
			return
		}
		operation, err := handler.controller.Status()
		if errors.Is(err, ErrNoOperation) {
			writeError(response, http.StatusNotFound, "operation_not_found")
			return
		}
		if err != nil {
			writeError(response, http.StatusServiceUnavailable, "operation_unavailable")
			return
		}
		writeJSON(response, http.StatusOK, operation)
	case "/v1/updates/apply":
		if request.Method != http.MethodPost {
			methodNotAllowed(response, http.MethodPost)
			return
		}
		var input updatecheck.ApplyRequest
		decoder := json.NewDecoder(io.LimitReader(request.Body, maximumRequestBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeError(response, http.StatusBadRequest, "invalid_request")
			return
		}
		operation, err := handler.controller.Apply(request.Context(), input)
		switch {
		case errors.Is(err, ErrHardwareConfirmationRequired):
			writeError(response, http.StatusConflict, "hardware_confirmation_required")
		case errors.Is(err, ErrOperationRunning):
			writeError(response, http.StatusConflict, "update_in_progress")
		case errors.Is(err, ErrNoUpdate), errors.Is(err, ErrVersionChanged):
			writeError(response, http.StatusConflict, "update_not_available")
		case err != nil:
			writeError(response, http.StatusServiceUnavailable, "updater_unavailable")
		default:
			writeJSON(response, http.StatusAccepted, operation)
		}
	case "/v1/updates/download":
		if request.Method != http.MethodPost {
			methodNotAllowed(response, http.MethodPost)
			return
		}
		if handler.downloader == nil {
			writeError(response, http.StatusServiceUnavailable, "download_unsupported")
			return
		}
		download, err := handler.downloader.Start(request.Context())
		switch {
		case errors.Is(err, ErrNothingToDownload):
			writeError(response, http.StatusConflict, "update_not_available")
		case err != nil:
			writeError(response, http.StatusServiceUnavailable, "updater_unavailable")
		default:
			writeJSON(response, http.StatusAccepted, download)
		}
	case "/v1/updates/preferences":
		if handler.preferences == nil {
			writeError(response, http.StatusServiceUnavailable, "download_unsupported")
			return
		}
		switch request.Method {
		case http.MethodGet:
			writeJSON(response, http.StatusOK, handler.preferences.Load())
		case http.MethodPut:
			var input updatecheck.Preferences
			decoder := json.NewDecoder(io.LimitReader(request.Body, maximumRequestBytes))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				writeError(response, http.StatusBadRequest, "invalid_request")
				return
			}
			if err := handler.preferences.Save(input); err != nil {
				writeError(response, http.StatusServiceUnavailable, "preferences_unavailable")
				return
			}
			writeJSON(response, http.StatusOK, handler.preferences.Load())
		default:
			methodNotAllowed(response, http.MethodGet+", "+http.MethodPut)
		}
	case "/v1/updates/running":
		if request.Method != http.MethodGet {
			methodNotAllowed(response, http.MethodGet)
			return
		}
		if handler.running == nil {
			writeError(response, http.StatusServiceUnavailable, "running_unavailable")
			return
		}
		images, err := handler.running.RunningImages(request.Context())
		if err != nil {
			writeError(response, http.StatusServiceUnavailable, "running_unavailable")
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"images": images})
	case "/v1/release/offer":
		if request.Method != http.MethodPost {
			methodNotAllowed(response, http.MethodPost)
			return
		}
		if handler.release == nil {
			writeError(response, http.StatusServiceUnavailable, "release_relay_unsupported")
			return
		}
		var input ReleaseOffer
		decoder := json.NewDecoder(io.LimitReader(request.Body, maximumOfferBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeError(response, http.StatusBadRequest, "invalid_request")
			return
		}
		var signed []byte
		if input.SignedBase64 != "" {
			decoded, err := base64.StdEncoding.DecodeString(input.SignedBase64)
			if err != nil {
				writeError(response, http.StatusBadRequest, "invalid_request")
				return
			}
			signed = decoded
		}
		source, err := handler.release.Offer(input.Kind, signed, input.Peers)
		if err != nil {
			writeError(response, http.StatusBadRequest, "invalid_request")
			return
		}
		writeJSON(response, http.StatusOK, source)
	case "/v1/release/signed":
		if request.Method != http.MethodGet {
			methodNotAllowed(response, http.MethodGet)
			return
		}
		if handler.release == nil {
			writeError(response, http.StatusServiceUnavailable, "release_relay_unsupported")
			return
		}
		signed := handler.release.Signed()
		if len(signed) == 0 {
			writeError(response, http.StatusNotFound, "release_not_found")
			return
		}
		writeJSON(response, http.StatusOK, ReleaseOffer{SignedBase64: base64.StdEncoding.EncodeToString(signed)})
	default:
		writeError(response, http.StatusNotFound, "not_found")
	}
}

func (handler *Handler) authorized(request *http.Request) bool {
	provided := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
	if len(provided) != len(handler.token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(handler.token)) == 1
}

func methodNotAllowed(response http.ResponseWriter, allowed string) {
	response.Header().Set("Allow", allowed)
	writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
}

func writeError(response http.ResponseWriter, status int, code string) {
	writeJSON(response, status, map[string]string{"code": code})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
