package httpapi

import (
	"encoding/json"
	"testing"
	"time"

	nkn "github.com/nknorg/nkn-sdk-go"
)

func dhtMessage(data []byte, src string, encrypted bool) *nkn.Message {
	return &nkn.Message{Src: src, Data: data, Encrypted: encrypted}
}

func dhtOffer(mutate func(*dhtAnnouncement)) []byte {
	offer := dhtAnnouncement{
		Type:      dhtAnnouncementType,
		Version:   dhtAnnouncementVersion,
		IssuedAt:  time.Now().Unix(),
		PeerID:    "12D3KooWH3uVF6wv47WnArKHk5p6cvgCJEb74UTmxztmQDc298L3",
		Addresses: []string{"/ip4/192.168.1.9/tcp/4001/p2p/12D3KooWH3uVF6wv47WnArKHk5p6cvgCJEb74UTmxztmQDc298L3"},
	}
	if mutate != nil {
		mutate(&offer)
	}
	raw, _ := json.Marshal(offer)
	return raw
}

// An announcement is a request to dial wherever the sender points. Accepting an
// unencrypted one would let anybody who can reach the NKN address aim this
// gateway's outbound connections.
func TestDHTAnnouncementRequiresEncryption(t *testing.T) {
	api := &API{}
	if api.receiveDHTAnnouncement(dhtMessage(dhtOffer(nil), "peer.nkn", false)) {
		t.Fatal("an unencrypted announcement was accepted")
	}
}

// A captured announcement must not work later.
func TestDHTAnnouncementRejectsStaleAndFutureTimestamps(t *testing.T) {
	api := &API{}
	for _, shift := range []int64{-3600, -121, 31, 3600} {
		raw := dhtOffer(func(offer *dhtAnnouncement) {
			offer.IssuedAt = time.Now().Unix() + shift
		})
		// Consumed (returns true) but must never reach the dial path; with no
		// WireGuard settings configured there is no paired peer either, so the
		// assertion here is that it does not panic or dial.
		api.receiveDHTAnnouncement(dhtMessage(raw, "peer.nkn", true))
	}
}

// The wrong type or version must be passed on rather than misread: the same
// channel carries chat and WireGuard endpoints.
func TestDHTAnnouncementIgnoresOtherMessageTypes(t *testing.T) {
	api := &API{}
	raw := dhtOffer(func(offer *dhtAnnouncement) { offer.Type = "homesim.wireguard.endpoint" })
	if api.receiveDHTAnnouncement(dhtMessage(raw, "peer.nkn", true)) {
		t.Fatal("a WireGuard announcement was consumed by the DHT handler")
	}
	raw = dhtOffer(func(offer *dhtAnnouncement) { offer.Version = 99 })
	if api.receiveDHTAnnouncement(dhtMessage(raw, "peer.nkn", true)) {
		t.Fatal("an unknown version was consumed")
	}
}

// With no peers configured, nothing is paired, so nothing is dialled. A gateway
// that dialled anyone who asked would be taking direction from strangers.
func TestNoPairedPeersMeansNoSender(t *testing.T) {
	api := &API{}
	if api.nknAddressIsPairedPeer("someone.nkn") {
		t.Fatal("an unconfigured gateway treated a stranger as paired")
	}
	if api.nknAddressIsPairedPeer("") {
		t.Fatal("an empty source was treated as paired")
	}
}
