package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Network intent and encrypted identity are independent from SMS destinations.
// Enabling the network never subscribes to a topic or forwards any message.
type homeSIMNKNSettings struct {
	Revision int64                `json:"revision"`
	Enabled  bool                 `json:"enabled"`
	Secret   *simAutoUnlockSecret `json:"secret,omitempty"`
}

type homeSIMNKNStatus struct {
	State     string    `json:"state"`
	Address   string    `json:"address,omitempty"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (api *API) loadHomeSIMNKNSettings() (homeSIMNKNSettings, string, error) {
	value := homeSIMNKNSettings{Revision: 1, Enabled: true}
	dir, ok := api.homeSIMDataDirectory()
	if !ok {
		return value, "", fmt.Errorf("服务器存储尚未就绪")
	}
	path := filepath.Join(dir, "nkn.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return value, path, nil
	}
	if err != nil {
		return value, path, err
	}
	err = json.Unmarshal(data, &value)
	if err == nil && value.Revision < 1 {
		err = fmt.Errorf("NKN 配置版本无效")
	}
	return value, path, err
}

func (api *API) managedNKNSeed() ([]byte, error) {
	api.homeSIMNKNSettingsMu.Lock()
	defer api.homeSIMNKNSettingsMu.Unlock()
	value, path, err := api.loadHomeSIMNKNSettings()
	if err != nil {
		return nil, err
	}
	if !value.Enabled {
		return nil, fmt.Errorf("NKN 网络已关闭")
	}
	if api.settingsSecrets == nil {
		return nil, fmt.Errorf("服务器尚未配置密钥保护")
	}
	if value.Secret != nil {
		seed, err := api.settingsSecrets.Open(value.Secret.Nonce, value.Secret.Ciphertext, []byte("homesim-nkn-identity-v1"))
		if err != nil {
			return nil, err
		}
		if len(seed) != 32 {
			return nil, fmt.Errorf("NKN 身份长度无效")
		}
		return seed, nil
	}
	// Preserve the gateway identity provisioned by an earlier deployment.
	if os.Getenv("HOMESIM_NKN_SEED") != "" {
		return resolveNKNSeed("HOMESIM_NKN_SEED")
	}
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	nonce, ciphertext, err := api.settingsSecrets.Seal(seed, []byte("homesim-nkn-identity-v1"))
	if err != nil {
		return nil, err
	}
	value.Secret = &simAutoUnlockSecret{Nonce: nonce, Ciphertext: ciphertext}
	if err := writeHomeSIMJSON(path, value); err != nil {
		return nil, err
	}
	return seed, nil
}

func (api *API) homeSIMNKNSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		writeError(w, 405, "method_not_allowed", "仅支持读取和保存", "")
		return
	}
	api.homeSIMNKNSettingsMu.Lock()
	defer api.homeSIMNKNSettingsMu.Unlock()
	value, path, err := api.loadHomeSIMNKNSettings()
	if err != nil {
		api.writeInternalError(w, r, "load NKN settings", err)
		return
	}
	if r.Method == http.MethodPut {
		var input struct {
			Revision int64 `json:"revision"`
			Enabled  bool  `json:"enabled"`
		}
		if !decodeJSONBody(w, r, &input) {
			return
		}
		if input.Revision != value.Revision {
			writeError(w, 409, "revision_conflict", "配置已更新，请刷新后重试", "revision")
			return
		}
		value.Revision++
		value.Enabled = input.Enabled
		if err := writeHomeSIMJSON(path, value); err != nil {
			api.writeInternalError(w, r, "save NKN settings", err)
			return
		}
	}
	api.homeSIMNKNStatusMu.Lock()
	status := api.homeSIMNKNStatus
	api.homeSIMNKNStatusMu.Unlock()
	if !value.Enabled {
		status.State = "disabled"
		status.Error = ""
	}
	if status.State == "" {
		status.State = "connecting"
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{
		"settings": map[string]any{"revision": value.Revision, "enabled": value.Enabled},
		"status":   status, "identity_managed": true, "website": "https://nkn.org/",
		"capabilities": map[string]bool{"sms_forwarding": true, "http_tunnel": false, "voice_transport": false},
	})
}

func (api *API) setNKNStatus(state, address, message string) {
	api.homeSIMNKNStatusMu.Lock()
	api.homeSIMNKNStatus = homeSIMNKNStatus{State: state, Address: address, Error: message, UpdatedAt: time.Now().UTC()}
	api.homeSIMNKNStatusMu.Unlock()
}

func (api *API) StartHomeSIMNKNWorker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		var current *homeSIMNKNClient
		var receiving *homeSIMNKNClient
		var lastAnnouncement time.Time
		defer func() {
			if current != nil && current.client != nil {
				_ = current.client.Close()
			}
		}()
		for {
			api.homeSIMNKNSettingsMu.Lock()
			settings, _, configErr := api.loadHomeSIMNKNSettings()
			api.homeSIMNKNSettingsMu.Unlock()
			if configErr != nil {
				api.setNKNStatus("error", "", "无法读取 NKN 配置，请检查服务器存储")
			} else if !settings.Enabled {
				if current != nil && current.client != nil {
					_ = current.client.Close()
					current = nil
				}
				api.setNKNStatus("disabled", "", "")
			} else {
				seed, err := api.managedNKNSeed()
				if err == nil {
					current, err = api.homeSIMNKNClient(seed)
				}
				if err != nil {
					api.setNKNStatus("error", "", "NKN 暂未连接，请检查网络和服务器密钥配置；系统会自动重连")
				} else {
					api.setNKNStatus("connected", current.address, "")
					if receiving != current {
						receiving = current
						go api.receiveNKNControl(ctx, current)
					}
					if time.Since(lastAnnouncement) >= time.Minute {
						api.announceWireGuardEndpoint(current)
						lastAnnouncement = time.Now()
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
