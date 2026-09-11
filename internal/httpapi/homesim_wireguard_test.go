package httpapi

import (
	"encoding/base64"
	"encoding/json"
	nkn "github.com/nknorg/nkn-sdk-go"
	"path/filepath"
	"testing"
	"time"
)

func TestWireGuardNKNAuthorizationAndReplay(t *testing.T) {
	api, err := New(&fakeRepository{}, Options{disableAuthentication: true,
		disableLicenseEnforcement: true, ContactBackupDirectory: filepath.Join(t.TempDir(), "backups", "contacts")})
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012"))
	settings := wireGuardSettings{Revision: 1, Enabled: true, Address: "10.77.0.1/24", ListenPort: 51820, Peers: []wireGuardPeer{{PublicKey: key, NKNAddress: "trusted.identity", AllowedIPs: []string{"10.77.0.2/32"}}}}
	dir, _ := api.wireGuardDirectory()
	if err := writeHomeSIMJSON(filepath.Join(dir, "settings.json"), settings); err != nil {
		t.Fatal(err)
	}
	offer := wireGuardAnnouncement{Type: "homesim.wireguard.endpoint", Version: 1, IssuedAt: time.Now().Unix(), PublicKey: key, Endpoint: "example.com:51820"}
	data, _ := json.Marshal(offer)
	for _, msg := range []*nkn.Message{{Src: "attacker", Encrypted: true, Data: data}, {Src: "trusted.identity", Data: data}} {
		api.receiveWireGuardAnnouncement(msg)
		got, _ := api.loadWireGuardSettings()
		if got.Revision != 1 {
			t.Fatal("unauthorized update")
		}
	}
	api.receiveWireGuardAnnouncement(&nkn.Message{Src: "trusted.identity", Encrypted: true, Data: data})
	got, _ := api.loadWireGuardSettings()
	if got.Revision != 2 || got.Peers[0].Endpoint != offer.Endpoint {
		t.Fatal("authorized update not applied")
	}
	api.receiveWireGuardAnnouncement(&nkn.Message{Src: "trusted.identity", Encrypted: true, Data: data})
	got, _ = api.loadWireGuardSettings()
	if got.Revision != 2 {
		t.Fatal("replay accepted")
	}
	offer.IssuedAt -= 300
	data, _ = json.Marshal(offer)
	api.receiveWireGuardAnnouncement(&nkn.Message{Src: "trusted.identity", Encrypted: true, Data: data})
	got, _ = api.loadWireGuardSettings()
	if got.Revision != 2 {
		t.Fatal("expired update accepted")
	}
}

func TestWireGuardRejectHostRoutes(t *testing.T) {
	s := wireGuardSettings{Address: "10.77.0.1/24", ListenPort: 51820, Peers: []wireGuardPeer{{PublicKey: base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012")), AllowedIPs: []string{"10.77.0.2/32"}}}}
	if !validWGSettings(s) {
		t.Fatal("valid client rejected")
	}
	for _, route := range []string{"0.0.0.0/0", "192.168.120.0/24", "10.77.0.1/32", "10.77.0.0/32", "10.77.0.255/32"} {
		s.Peers[0].AllowedIPs = []string{route}
		if validWGSettings(s) {
			t.Fatalf("unsafe route accepted: %s", route)
		}
	}
}
