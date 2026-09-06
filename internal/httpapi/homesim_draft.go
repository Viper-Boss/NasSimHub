package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Saved adapter configuration is explicitly a draft. No runtime executor is
// advertised until audio ownership, filtering and push workers are implemented.
// Only environment-variable references are accepted, never API keys or PINs.
type homeSIMIntegrationDraft struct {
	Revision int64 `json:"revision"`
	Spam     struct {
		Provider      string   `json:"provider"`
		CredentialRef string   `json:"credential_ref"`
		MinReports    int      `json:"min_reports"`
		Blacklist     []string `json:"blacklist"`
		Whitelist     []string `json:"whitelist"`
	} `json:"spam"`
	Identity struct {
		Provider      string `json:"provider"`
		CredentialRef string `json:"credential_ref"`
	} `json:"identity"`
	AI struct {
		Endpoint           string `json:"endpoint"`
		CredentialRef      string `json:"credential_ref"`
		Model              string `json:"model"`
		Greeting           string `json:"greeting"`
		AnswerAfterSeconds int    `json:"answer_after_seconds"`
		MaxDurationSeconds int    `json:"max_duration_seconds"`
		Transcript         bool   `json:"transcript"`
		Summary            bool   `json:"summary"`
		PushChannel        string `json:"push_channel"`
	} `json:"ai"`
}

func defaultHomeSIMDraft() homeSIMIntegrationDraft {
	var draft homeSIMIntegrationDraft
	draft.Revision = 1
	draft.Spam.Provider = "local"
	draft.Spam.CredentialRef = "HOMESIM_SPAM_API_KEY"
	draft.Spam.MinReports = 10
	draft.Spam.Blacklist = []string{}
	draft.Spam.Whitelist = []string{}
	draft.Identity.Provider = "off"
	draft.Identity.CredentialRef = "HOMESIM_IDENTITY_API_KEY"
	draft.AI.CredentialRef = "HOMESIM_AI_API_KEY"
	draft.AI.Greeting = "您好，我是机主的 AI 电话助理。本次通话将录音并转成文字，请问有什么事需要转达？"
	draft.AI.AnswerAfterSeconds = 20
	draft.AI.MaxDurationSeconds = 180
	draft.AI.Transcript = true
	draft.AI.Summary = true
	draft.AI.PushChannel = "none"
	return draft
}

var homeSIMCredentialName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,79}$`)
var homeSIMPhone = regexp.MustCompile(`^\+?[0-9]{3,20}$`)

func validHomeSIMDraft(d homeSIMIntegrationDraft) bool {
	if d.Revision < 1 || (d.Spam.Provider != "local" && d.Spam.Provider != "juhe") ||
		(d.Identity.Provider != "off" && d.Identity.Provider != "juhe") ||
		(d.AI.PushChannel != "none" && d.AI.PushChannel != "telegram") ||
		d.Spam.MinReports < 1 || d.Spam.MinReports > 10000 ||
		d.AI.AnswerAfterSeconds < 5 || d.AI.AnswerAfterSeconds > 60 ||
		d.AI.MaxDurationSeconds < 30 || d.AI.MaxDurationSeconds > 600 ||
		strings.TrimSpace(d.AI.Greeting) == "" || utf8.RuneCountInString(d.AI.Greeting) > 500 ||
		len(d.AI.Model) > 100 || len(d.AI.Endpoint) > 2048 {
		return false
	}
	for _, name := range []string{d.Spam.CredentialRef, d.Identity.CredentialRef, d.AI.CredentialRef} {
		if !homeSIMCredentialName.MatchString(name) {
			return false
		}
	}
	if len(d.Spam.Blacklist) > 500 || len(d.Spam.Whitelist) > 500 {
		return false
	}
	seen := map[string]bool{}
	for _, number := range append(append([]string{}, d.Spam.Blacklist...), d.Spam.Whitelist...) {
		if !homeSIMPhone.MatchString(number) || seen[number] {
			return false
		}
		seen[number] = true
	}
	if d.AI.Endpoint != "" {
		endpoint, err := url.Parse(d.AI.Endpoint)
		if err != nil || (endpoint.Scheme != "https" && endpoint.Scheme != "wss") || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
			return false
		}
	}
	return true
}

func (api *API) homeSIMDraft(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodPut {
		response.Header().Set("Allow", "GET, PUT")
		writeError(response, 405, "method_not_allowed", "Only GET and PUT are supported", "")
		return
	}
	if api.contactBackupDirectory == "" {
		writeError(response, 503, "integration_unavailable", "HomeSIM server storage is unavailable", "")
		return
	}
	api.homeSIMDraftMu.Lock()
	defer api.homeSIMDraftMu.Unlock()
	directory := filepath.Join(filepath.Dir(filepath.Dir(api.contactBackupDirectory)), "homesim")
	path := filepath.Join(directory, "integration-draft.json")
	current := defaultHomeSIMDraft()
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &current)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		api.writeInternalError(response, request, "read HomeSIM draft", err)
		return
	}
	if request.Method == http.MethodPut {
		var next homeSIMIntegrationDraft
		if !decodeJSONBody(response, request, &next) {
			return
		}
		if !validHomeSIMDraft(next) {
			writeError(response, 400, "invalid_argument", "Invalid HomeSIM adapter draft; provide environment variable names, not credentials", "draft")
			return
		}
		if next.Revision != current.Revision {
			writeError(response, 409, "revision_conflict", "Draft changed; reload before saving", "revision")
			return
		}
		next.Revision++
		if next.Spam.Blacklist == nil {
			next.Spam.Blacklist = []string{}
		}
		if next.Spam.Whitelist == nil {
			next.Spam.Whitelist = []string{}
		}
		if err := os.MkdirAll(directory, 0700); err != nil {
			api.writeInternalError(response, request, "prepare HomeSIM draft", err)
			return
		}
		file, err := os.CreateTemp(directory, ".draft-*")
		if err != nil {
			api.writeInternalError(response, request, "create HomeSIM draft", err)
			return
		}
		defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
		err = json.NewEncoder(file).Encode(next)
		if err == nil {
			err = file.Sync()
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(file.Name(), path)
		}
		if err != nil {
			api.writeInternalError(response, request, "save HomeSIM draft", err)
			return
		}
		current = next
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, 200, map[string]any{"draft": current, "state": "draft_only", "runtime_ready": false})
}
