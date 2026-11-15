package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// HomeSIM SMS forwarding channels. Settings follow the NasSimHub integration
// rules: the channel list itself is server-side state (forwarders.json), and
// secret-looking values (ServerChan/PushPlus keys) are stored only as the
// name of the environment variable holding them, resolved at send time.

const maxForwarderChannels = 8

var forwarderTypes = map[string]bool{
	"webhook":    true,
	"dingtalk":   true,
	"feishu":     true,
	"wecom":      true,
	"serverchan": true,
	"pushplus":   true,
	"nkn":        true,
}

type homeSIMForwarderConfig struct {
	WebhookURL    string `json:"webhook_url,omitempty"`
	Secret        string `json:"secret,omitempty"`
	SecretSet     bool   `json:"secret_set,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty"`
	NKNTopic      string `json:"nkn_topic,omitempty"`
	NKNAddresses  string `json:"nkn_addresses,omitempty"`
}

type homeSIMForwarder struct {
	ID      string                 `json:"id"`
	Name    string                 `json:"name"`
	Type    string                 `json:"type"`
	Enabled bool                   `json:"enabled"`
	Events  []string               `json:"events"`
	Config  homeSIMForwarderConfig `json:"config"`
}

type homeSIMForwarderSettings struct {
	Revision   int64              `json:"revision"`
	Forwarders []homeSIMForwarder `json:"forwarders"`
}

func defaultHomeSIMForwarderSettings() homeSIMForwarderSettings {
	return homeSIMForwarderSettings{Revision: 1, Forwarders: []homeSIMForwarder{}}
}

func validHomeSIMForwarderSettings(settings homeSIMForwarderSettings) bool {
	if len(settings.Forwarders) > maxForwarderChannels {
		return false
	}
	seen := map[string]bool{}
	for _, forwarder := range settings.Forwarders {
		if forwarder.ID == "" || len(forwarder.ID) > 64 || seen[forwarder.ID] {
			return false
		}
		seen[forwarder.ID] = true
		if len(forwarder.Name) > 64 {
			return false
		}
		if !forwarderTypes[forwarder.Type] {
			return false
		}
		if len(forwarder.Events) == 0 {
			return false
		}
		for _, event := range forwarder.Events {
			if event != "sms" {
				return false
			}
		}
		if !validHomeSIMForwarderConfig(forwarder.Type, forwarder.Config) {
			return false
		}
	}
	return true
}

func validHomeSIMForwarderConfig(kind string, config homeSIMForwarderConfig) bool {
	switch kind {
	case "webhook", "feishu", "wecom":
		return httpsOriginOnly(config.WebhookURL)
	case "dingtalk":
		if !httpsOriginOnly(config.WebhookURL) {
			return false
		}
		return len(config.Secret) <= 128
	case "serverchan", "pushplus":
		if config.CredentialRef == "" || len(config.CredentialRef) > 128 {
			return false
		}
		return !strings.ContainsAny(config.CredentialRef, " \t\r\n=")
	case "nkn":
		if config.CredentialRef == "" || len(config.CredentialRef) > 128 {
			return false
		}
		if strings.ContainsAny(config.CredentialRef, " \t\r\n=") {
			return false
		}
		topic := strings.TrimSpace(config.NKNTopic)
		addresses := nknAddressList(config.NKNAddresses)
		if topic == "" && len(addresses) == 0 {
			return false
		}
		if len(topic) > 64 || strings.ContainsAny(topic, " \t\r\n#") {
			return false
		}
		for _, address := range addresses {
			if len(address) > 96 {
				return false
			}
			if strings.ContainsAny(address, " \t\r\n") {
				return false
			}
		}
		return true
	}
	return false
}

func httpsOriginOnly(raw string) bool {
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return false
	}
	return len(raw) <= 1024
}

func (api *API) homeSIMForwarderPath() (string, bool) {
	directory, ok := api.homeSIMDataDirectory()
	if !ok {
		return "", false
	}
	return filepath.Join(directory, "forwarders.json"), true
}

func (api *API) loadHomeSIMForwarderSettings() homeSIMForwarderSettings {
	current := defaultHomeSIMForwarderSettings()
	path, ok := api.homeSIMForwarderPath()
	if !ok {
		return current
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return current
	}
	var loaded homeSIMForwarderSettings
	if err := json.Unmarshal(data, &loaded); err != nil {
		return defaultHomeSIMForwarderSettings()
	}
	if loaded.Forwarders == nil {
		loaded.Forwarders = []homeSIMForwarder{}
	}
	if loaded.Revision < 1 {
		loaded.Revision = 1
	}
	return loaded
}

func (api *API) homeSIMForwarders(w http.ResponseWriter, r *http.Request)    { forwardingReplaced(w) }
func (api *API) homeSIMForwarderTest(w http.ResponseWriter, r *http.Request) { forwardingReplaced(w) }
