package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	nkn "github.com/nknorg/nkn-sdk-go"
)

// Bootstrapping the DHT over NKN.
//
// A DHT cannot find a network it has no entry point into -- the handoff is
// blunt about that in section 14, and about not pretending otherwise. The entry
// points it lists are a bootstrap address typed in by hand, a paired QR, a
// cached peer, mDNS on the LAN, and this one: NKN carrying the libp2p addresses
// of instances you already know.
//
// NKN is the right carrier because the two paths fail for different reasons.
// NKN is a blockchain overlay reached through public seeds; the DHT is direct
// peer-to-peer. Whichever is reachable can hand over the addresses the other
// needs, which is the whole point of running two control paths.
//
// What this deliberately does NOT do is publish addresses to the shared public
// topic. The subscriber list is public and permanent, and a multiaddr in it
// would put this NAS's address on a public chain forever, undoing the property
// that the directory lists "an anonymous NKN public key with no real-world
// attachment". These announcements are encrypted point-to-point messages to
// instances already configured as peers, so an address only ever reaches
// somebody who was already told who this gateway is.

// dhtAnnouncement is signalling only. It grants nothing: a receiver still puts
// every dialled peer through the connection gate, so an address arriving here
// is an invitation to try, not permission to connect.
type dhtAnnouncement struct {
	Type      string   `json:"type"`
	Version   int      `json:"version"`
	IssuedAt  int64    `json:"issued_at"`
	PeerID    string   `json:"peer_id"`
	Addresses []string `json:"addresses"`
}

const (
	dhtAnnouncementType    = "homesim.dht.bootstrap"
	dhtAnnouncementVersion = 1
	// A peer publishes a handful of listen addresses at most. The cap stops a
	// single message turning into an unbounded pile of outbound dials.
	dhtAnnouncementMaxAddresses = 6
	dhtAnnouncementMaxBytes     = 2048
)

// receiveDHTAnnouncement dials the libp2p addresses a paired instance sent.
//
// Every check here mirrors the WireGuard endpoint announcement beside it, for
// the same reasons: encrypted so the sender's NKN identity is proven, bounded
// so a large message cannot be used as an amplifier, typed and versioned so a
// future format cannot be misread as this one, and time-windowed so a captured
// message cannot be replayed later.
func (api *API) receiveDHTAnnouncement(message *nkn.Message) bool {
	if message == nil || !message.Encrypted || len(message.Data) > dhtAnnouncementMaxBytes {
		return false
	}
	var offer dhtAnnouncement
	if json.Unmarshal(message.Data, &offer) != nil ||
		offer.Type != dhtAnnouncementType ||
		offer.Version != dhtAnnouncementVersion ||
		offer.PeerID == "" ||
		len(offer.Addresses) == 0 ||
		len(offer.Addresses) > dhtAnnouncementMaxAddresses {
		return false
	}
	now := time.Now().Unix()
	if offer.IssuedAt < now-120 || offer.IssuedAt > now+30 {
		return true
	}

	// Only from an instance already configured as a peer. An address from a
	// stranger is not a bootstrap hint, it is a request to dial wherever they
	// point us.
	if !api.nknAddressIsPairedPeer(message.Src) {
		return true
	}

	addresses := make([]string, 0, len(offer.Addresses))
	for _, address := range offer.Addresses {
		address = strings.TrimSpace(address)
		// The identity has to be in the address. Without /p2p/<id> this names a
		// host and port with no statement about who answers there, which is
		// exactly what must not be dialled on someone else's say-so.
		if address == "" || len(address) > 200 || !strings.HasPrefix(address, "/") {
			continue
		}
		if !strings.Contains(address, "/p2p/"+offer.PeerID) {
			continue
		}
		addresses = append(addresses, address)
	}
	if len(addresses) == 0 {
		return true
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if api.dhtService == nil {
			return
		}
		if _, err := api.dhtService.DialDHT(ctx, addresses); err != nil {
			api.logger.Debug(
				"could not dial a DHT peer announced over NKN",
				"peer", offer.PeerID,
				"error", err,
			)
		}
	}()
	return true
}

// nknAddressIsPairedPeer reports whether src belongs to an instance already
// configured as a WireGuard peer.
//
// That list is what "a partner we have paired with" already means in this
// system -- the same addresses the WireGuard endpoint announcements trust --
// so the DHT reuses it rather than inventing a second notion of pairing that
// could drift out of step with the first.
func (api *API) nknAddressIsPairedPeer(src string) bool {
	src = strings.TrimSpace(src)
	if src == "" {
		return false
	}
	api.wireGuardMu.Lock()
	defer api.wireGuardMu.Unlock()
	settings, err := api.loadWireGuardSettings()
	if err != nil {
		return false
	}
	for _, peer := range settings.Peers {
		if peer.NKNAddress != "" && peer.NKNAddress == src {
			return true
		}
	}
	return false
}

// announceDHTAddresses tells each paired instance where to reach this node's
// libp2p host.
//
// Nothing is sent unless the host is actually up with addresses to give: an
// announcement for a node that is not listening would have peers dial a closed
// port and conclude the DHT is broken.
func (api *API) announceDHTAddresses(client *homeSIMNKNClient) {
	if api.dhtService == nil {
		return
	}
	api.dhtMu.Lock()
	settings, err := api.loadDHTSettings()
	api.dhtMu.Unlock()
	if err != nil || !settings.Enabled {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	status, err := api.dhtService.DHTStatus(ctx, true)
	cancel()
	if err != nil || status.PeerID == "" || len(status.Addresses) == 0 {
		return
	}

	addresses := make([]string, 0, dhtAnnouncementMaxAddresses)
	for _, address := range status.Addresses {
		// Loopback tells a remote peer nothing; it would only ever dial itself.
		if strings.HasPrefix(address, "/ip4/127.") || strings.HasPrefix(address, "/ip6/::1") {
			continue
		}
		addresses = append(addresses, address+"/p2p/"+status.PeerID)
		if len(addresses) == dhtAnnouncementMaxAddresses {
			break
		}
	}
	if len(addresses) == 0 {
		return
	}

	data, err := json.Marshal(dhtAnnouncement{
		Type:      dhtAnnouncementType,
		Version:   dhtAnnouncementVersion,
		IssuedAt:  time.Now().Unix(),
		PeerID:    status.PeerID,
		Addresses: addresses,
	})
	if err != nil {
		return
	}

	api.wireGuardMu.Lock()
	wireGuard, wgErr := api.loadWireGuardSettings()
	api.wireGuardMu.Unlock()
	if wgErr != nil {
		return
	}
	for _, peer := range wireGuard.Peers {
		if peer.NKNAddress == "" {
			continue
		}
		_, _ = client.client.Send(
			nkn.NewStringArray(peer.NKNAddress),
			data,
			&nkn.MessageConfig{NoReply: true, MaxHoldingSeconds: 60},
		)
	}
}
