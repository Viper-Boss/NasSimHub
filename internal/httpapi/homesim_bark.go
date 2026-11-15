package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Bark is an iOS push channel. Following the NasSimHub integration rules the
// device key itself is never stored on disk: only the name of the server
// environment variable holding it (credential_ref), resolved at send time.

const defaultBarkServerURL = "https://api.day.app"

type homeSIMBarkSettings struct {
	Revision      int64  `json:"revision"`
	ServerURL     string `json:"server_url"`
	CredentialRef string `json:"credential_ref"`
	Group         string `json:"group"`
	Sound         string `json:"sound"`
}

func defaultHomeSIMBarkSettings() homeSIMBarkSettings {
	return homeSIMBarkSettings{
		Revision:      1,
		ServerURL:     defaultBarkServerURL,
		CredentialRef: "HOMESIM_BARK_KEY",
		Group:         "NasSimHub",
		Sound:         "",
	}
}

func validHomeSIMBarkSettings(settings homeSIMBarkSettings) bool {
	if settings.CredentialRef == "" || len(settings.CredentialRef) > 128 {
		return false
	}
	if strings.ContainsAny(settings.CredentialRef, " \t\r\n=") {
		return false
	}
	endpoint, err := url.Parse(settings.ServerURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return false
	}
	return len(settings.Group) <= 32 && len(settings.Sound) <= 64
}

func (api *API) homeSIMBarkPath() (string, bool) {
	directory, ok := api.homeSIMDataDirectory()
	if !ok {
		return "", false
	}
	return filepath.Join(directory, "bark.json"), true
}

func (api *API) loadHomeSIMBarkSettings() homeSIMBarkSettings {
	current := defaultHomeSIMBarkSettings()
	path, ok := api.homeSIMBarkPath()
	if !ok {
		return current
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return current
	}
	var loaded homeSIMBarkSettings
	if err := json.Unmarshal(data, &loaded); err != nil || loaded.ServerURL == "" {
		return defaultHomeSIMBarkSettings()
	}
	if loaded.Revision < 1 {
		loaded.Revision = 1
	}
	return loaded
}

func (api *API) homeSIMBark(w http.ResponseWriter, r *http.Request)     { forwardingReplaced(w) }
func (api *API) homeSIMBarkTest(w http.ResponseWriter, r *http.Request) { forwardingReplaced(w) }
