package httpapi

import (
	"net/http"
	"strings"

	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/modemdeck/internal/release"
)

// Link boundaries.
//
// "Is this post-quantum?" has no single answer for the product: a request
// crosses several links, each protected by something different, and a hybrid
// handshake on one says nothing about the next. This file lists them one by
// one, each with what the gateway actually knows about it and how it knows.
//
// Evidence is one of:
//
//	observed   the gateway saw it on this request or on its own session
//	reported   the other end says so; the gateway did not see it itself
//	protocol   a fixed property of the protocol in use, not a measurement
//	not_used   the link is not part of this deployment or this request
//	unknown    the gateway has no way to tell
//
// PostQuantum is yes, no or unknown - never a default. A boundary whose key
// agreement is classical says "no", including the ones nothing can change
// today (WebRTC), because a blank there would read as "fine".
type linkBoundary struct {
	ID          string `json:"id"`
	Evidence    string `json:"evidence"`
	Protection  string `json:"protection,omitempty"`
	PostQuantum string `json:"post_quantum"`
	// Requested is the policy asked for and Negotiated what was obtained,
	// kept apart so a shortfall is visible rather than averaged away.
	Requested  string `json:"requested,omitempty"`
	Negotiated string `json:"negotiated,omitempty"`
	MeetsLevel *bool  `json:"meets_requested,omitempty"`
	NodeID     string `json:"node_id,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// Tailscale Serve terminates the browser's TLS itself and forwards to the
// gateway with these headers. They are the only evidence the API has that the
// request came that way. A client reaching the gateway directly can forge
// them, and gains only a wrong description of its own connection.
var tailscaleServeHeaders = []string{"Tailscale-User-Login", "Tailscale-User-Name", "Tailscale-Headers-Info"}

func viaTailscaleServe(header http.Header) bool {
	for _, name := range tailscaleServeHeaders {
		if strings.TrimSpace(header.Get(name)) != "" {
			return true
		}
	}
	return false
}

type boundaryNodes interface {
	Nodes() []nassimhubnode.NodeView
}

func yesNoUnknown(known, yes bool) string {
	switch {
	case !known:
		return "unknown"
	case yes:
		return "yes"
	}
	return "no"
}

// linkBoundaries builds the list for one request. Gateway-wide entries (Nodes,
// updates) are included for administrators only.
func (api *API) linkBoundaries(request *http.Request, connection edgeConnection) []linkBoundary {
	boundaries := []linkBoundary{}
	tailscale := connection.edge == edgeDirect && viaTailscaleServe(request.Header)

	browser := linkBoundary{ID: "browser_gateway", Evidence: "unknown", PostQuantum: "unknown"}
	switch {
	case tailscale:
		// What nginx negotiated was with Tailscale's proxy on this NAS, not
		// with the browser. The browser's own handshake ended at Tailscale.
		browser.Detail = "the browser's TLS ends at Tailscale Serve; its handshake is not visible to the gateway"
	case connection.edge == edgeCloudflare:
		browser.Detail = "the browser's TLS ends at Cloudflare; its handshake is not visible to the gateway"
	case connection.edge == edgeDirect && connection.protocol != "":
		row := postQuantumRow(connection.group)
		browser.Evidence, browser.Protection, browser.Negotiated = "observed", transportRow(connection.protocol).Value, row.Detail
		browser.PostQuantum = map[string]string{"ok": "yes", "off": "no"}[row.State]
		if browser.PostQuantum == "" {
			browser.PostQuantum = "unknown"
		}
	case connection.edge == edgeDirect:
		browser.Evidence, browser.Protection, browser.PostQuantum = "observed", "none (HTTP)", "no"
	}
	boundaries = append(boundaries, browser)

	proxy := linkBoundary{ID: "tailscale", Evidence: "not_used", PostQuantum: "unknown",
		Detail: "this request did not arrive through Tailscale Serve"}
	if tailscale {
		proxy = linkBoundary{ID: "tailscale", Evidence: "reported", Protection: "WireGuard (Tailscale)", PostQuantum: "no",
			Detail: "indicated by Tailscale Serve request headers; the tunnel's key agreement is Tailscale's own and is not post-quantum as far as this gateway can state"}
		hop := linkBoundary{ID: "tailscale_gateway", Evidence: "observed", Protection: transportRow(connection.protocol).Value,
			Negotiated: postQuantumRow(connection.group).Detail, PostQuantum: "unknown",
			Detail: "the local hop from Tailscale Serve to the gateway on this NAS"}
		if connection.protocol != "" {
			hop.PostQuantum = yesNoUnknown(postQuantumRow(connection.group).State != "unknown", postQuantumRow(connection.group).State == "ok")
		}
		boundaries = append(boundaries, proxy, hop)
	} else {
		boundaries = append(boundaries, proxy)
	}

	boundaries = append(boundaries, linkBoundary{ID: "call_media", Evidence: "protocol", Protection: "WebRTC DTLS-SRTP", PostQuantum: "no",
		Detail: "browser call audio is always DTLS-SRTP; its key agreement is classical ECDHE in every current browser"})

	if !requestIsAdmin(request) {
		return boundaries
	}
	if fleet, ok := api.nodeFleet.(boundaryNodes); ok && fleet != nil {
		for _, node := range fleet.Nodes() {
			link := node.LinkSecurity
			entry := linkBoundary{ID: "gateway_node", NodeID: node.DeviceID, Evidence: "unknown", PostQuantum: "unknown",
				Requested: link.RequestedLevel, Protection: link.TLSVersion, Negotiated: link.Group}
			if node.Online && link.Known {
				meets := link.MeetsLevel
				entry.Evidence, entry.MeetsLevel = "observed", &meets
				entry.PostQuantum = map[string]string{"yes": "yes", "no": "no"}[link.PostQuantum]
				if entry.PostQuantum == "" {
					entry.PostQuantum = "unknown"
				}
				if link.NodeGroup != "" && link.NodeGroup != link.Group {
					entry.Detail = "the device reports a different group (" + link.NodeGroup + ") than the gateway observed"
				}
			} else if !node.Online {
				entry.Detail = "the device is not connected; nothing is claimed about a session that is not up"
			}
			boundaries = append(boundaries, entry)
			identity := node.Identity
			boundaries = append(boundaries, linkBoundary{ID: "node_identity", NodeID: node.DeviceID,
				Evidence:    map[bool]string{true: "observed", false: "unknown"}[identity.Outcome != "" && identity.Outcome != "unknown"],
				Protection:  strings.TrimSpace("Ed25519 pin " + map[bool]string{true: "+ " + identity.Algorithm + " pin", false: ""}[identity.PinState == "pinned"]),
				PostQuantum: yesNoUnknown(identity.Outcome != "" && identity.Outcome != "unknown", identity.Outcome == "dual"),
				Requested:   identity.Policy, Negotiated: identity.Outcome, Detail: identity.Reason})
		}
		// KCP is a Node transport option the gateway does not use: every Node
		// session here is TLS over TCP on USB or Wi-Fi.
		boundaries = append(boundaries, linkBoundary{ID: "kcp", Evidence: "not_used", PostQuantum: "unknown",
			Detail: "this gateway connects to Nodes over TLS/TCP only"})
	}
	gatewayUpdates := linkBoundary{ID: "gateway_update", Evidence: "protocol", Protection: "Ed25519 release manifest", PostQuantum: "no",
		Detail: "the release manifest signature is always required"}
	if release.PQSigningPublicKey != "" {
		gatewayUpdates.Protection, gatewayUpdates.PostQuantum = "Ed25519 + "+release.PQSigningAlgorithm+" release manifest", "yes"
		gatewayUpdates.Requested = string(release.PQSignaturePolicy())
	}
	boundaries = append(boundaries, gatewayUpdates)
	if updates, ok := api.nodeFleet.(nodeUpdateService); ok {
		catalog := updates.NodeReleases()
		entry := linkBoundary{ID: "node_update", Evidence: "protocol", Protection: "Ed25519 release manifest, verified on the device",
			PostQuantum: "unknown", Detail: "each device verifies against its own release keys; whether a release is dual-signed is shown per release"}
		if !catalog.GatewayVerifies {
			entry.Detail += "; this gateway has no release keys and verifies nothing itself"
		}
		boundaries = append(boundaries, entry)
	}
	return boundaries
}
