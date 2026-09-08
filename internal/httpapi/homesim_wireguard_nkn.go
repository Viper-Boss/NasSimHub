package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	nkn "github.com/nknorg/nkn-sdk-go"
)

func (api *API) receiveNKNControl(ctx context.Context, client *homeSIMNKNClient) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if client.client.IsClosed() {
				return
			}
		case message, ok := <-client.client.OnMessage.C:
			if !ok {
				return
			}
			api.receiveWireGuardAnnouncement(message)
		}
	}
}

// This protocol is signalling only: it never grants access or changes routes.
// Both the encrypted NKN identity and pinned WireGuard key must already match.
type wireGuardAnnouncement struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	IssuedAt  int64  `json:"issued_at"`
	PublicKey string `json:"public_key"`
	Endpoint  string `json:"endpoint"`
}

func (api *API) receiveWireGuardAnnouncement(message *nkn.Message) {
	if message == nil || !message.Encrypted || len(message.Data) > 2048 {
		return
	}
	var offer wireGuardAnnouncement
	if json.Unmarshal(message.Data, &offer) != nil || offer.Type != "homesim.wireguard.endpoint" || offer.Version != 1 || offer.Endpoint == "" || !validWGEndpoint(offer.Endpoint) {
		return
	}
	now := time.Now().Unix()
	if offer.IssuedAt < now-120 || offer.IssuedAt > now+30 {
		return
	}
	api.wireGuardMu.Lock()
	defer api.wireGuardMu.Unlock()
	settings, err := api.loadWireGuardSettings()
	if err != nil || !settings.Enabled {
		return
	}
	for index := range settings.Peers {
		peer := &settings.Peers[index]
		if peer.NKNAddress == "" || peer.NKNAddress != message.Src || peer.PublicKey != offer.PublicKey || offer.IssuedAt <= peer.LastNKNUpdate {
			continue
		}
		peer.Endpoint = offer.Endpoint
		peer.LastNKNUpdate = offer.IssuedAt
		settings.Revision++
		dir, err := api.wireGuardDirectory()
		if err == nil {
			_ = writeHomeSIMJSON(filepath.Join(dir, "settings.json"), settings)
		}
		return
	}
}

func (api *API) announceWireGuardEndpoint(client *homeSIMNKNClient) {
	api.wireGuardMu.Lock()
	settings, err := api.loadWireGuardSettings()
	dir, _ := api.wireGuardDirectory()
	api.wireGuardMu.Unlock()
	if err != nil || !settings.Enabled || settings.AdvertisedEndpoint == "" {
		return
	}
	var status wireGuardStatus
	raw, err := os.ReadFile(filepath.Join(dir, "status.json"))
	if err != nil || json.Unmarshal(raw, &status) != nil || status.State != "running" || status.Revision != settings.Revision || time.Since(status.UpdatedAt) > 30*time.Second || status.PublicKey == "" {
		return
	}
	data, _ := json.Marshal(wireGuardAnnouncement{Type: "homesim.wireguard.endpoint", Version: 1, IssuedAt: time.Now().Unix(), PublicKey: status.PublicKey, Endpoint: settings.AdvertisedEndpoint})
	for _, peer := range settings.Peers {
		if peer.NKNAddress != "" {
			_, _ = client.client.Send(nkn.NewStringArray(peer.NKNAddress), data, &nkn.MessageConfig{NoReply: true, MaxHoldingSeconds: 60})
		}
	}
}
