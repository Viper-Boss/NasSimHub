package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/auth"
	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
)

type boundaryFleet struct {
	fakeNodeFleet
	nodes []nassimhubnode.NodeView
}

func (f *boundaryFleet) Nodes() []nassimhubnode.NodeView { return f.nodes }

func boundariesOf(t *testing.T, api *API, headers map[string]string, principal *auth.Principal) (map[string][]linkBoundary, map[string]json.RawMessage) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/security/posture", nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	if principal != nil {
		request = request.WithContext(auth.ContextWithPrincipal(context.Background(), *principal))
	}
	w := httptest.NewRecorder()
	api.securityPosture(w, request)
	var body map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var list []linkBoundary
	if err := json.Unmarshal(body["boundaries"], &list); err != nil {
		t.Fatalf("boundaries: %v %s", err, w.Body.String())
	}
	byID := map[string][]linkBoundary{}
	for _, boundary := range list {
		byID[boundary.ID] = append(byID[boundary.ID], boundary)
		switch boundary.PostQuantum {
		case "yes", "no", "unknown":
		default:
			t.Fatalf("%s: post_quantum is %q; it is always yes, no or unknown", boundary.ID, boundary.PostQuantum)
		}
		if boundary.Evidence == "" {
			t.Fatalf("%s carries no evidence label", boundary.ID)
		}
	}
	return byID, body
}

func TestEachLinkIsReportedOnItsOwnAndAHybridBrowserHandshakeSaysNothingAboutTheRest(t *testing.T) {
	meets := true
	fleet := &boundaryFleet{nodes: []nassimhubnode.NodeView{
		{DeviceID: "NSH-410-AAAAAA", Online: true,
			LinkSecurity: nassimhubnode.LinkSecurityView{Known: true, TLSVersion: "TLS 1.3", Group: "X25519", PostQuantum: "no", RequestedLevel: "PQ", MeetsLevel: false},
			Identity:     nassimhubnode.IdentityView{Outcome: "classical_only", Policy: "preferred", PinState: "none"}},
		{DeviceID: "NSH-410-BBBBBB", Online: true,
			LinkSecurity: nassimhubnode.LinkSecurityView{Known: true, TLSVersion: "TLS 1.3", Group: "X25519MLKEM768", PostQuantum: "yes", RequestedLevel: "PQ", MeetsLevel: meets},
			Identity:     nassimhubnode.IdentityView{Outcome: "dual", Policy: "required", PinState: "pinned", Algorithm: "ml-dsa-65"}},
		{DeviceID: "NSH-410-CCCCCC", Online: false,
			LinkSecurity: nassimhubnode.LinkSecurityView{Known: true, Group: "X25519MLKEM768", PostQuantum: "yes", RequestedLevel: "PQ"}},
	}}
	api := &API{nodeFleet: fleet}
	hybrid := map[string]string{edgeHeader: edgeDirect, edgeTLSProtocol: "TLSv1.3", edgeTLSGroup: "X25519MLKEM768", edgeTLSCipher: "TLS_AES_256_GCM_SHA384"}
	byID, body := boundariesOf(t, api, hybrid, nil)

	if got := byID["browser_gateway"][0]; got.PostQuantum != "yes" || got.Evidence != "observed" || got.Negotiated != "X25519MLKEM768" {
		t.Fatalf("browser link: %+v", got)
	}
	if _, present := body["transport_scope"]; present {
		t.Fatal("a direct request is not a proxy hop")
	}
	if got := byID["tailscale"][0]; got.Evidence != "not_used" {
		t.Fatalf("tailscale on a direct request: %+v", got)
	}
	if got := byID["call_media"][0]; got.PostQuantum != "no" || got.Evidence != "protocol" {
		t.Fatalf("call media is classical and is said to be: %+v", got)
	}
	if got := byID["gateway_update"][0]; got.PostQuantum != "no" {
		t.Fatalf("this build verifies classical release manifests only, and must say so: %+v", got)
	}
	if got := byID["kcp"][0]; got.Evidence != "not_used" {
		t.Fatalf("kcp: %+v", got)
	}
	links := byID["gateway_node"]
	if len(links) != 3 {
		t.Fatalf("one entry per device, have %d", len(links))
	}
	if links[0].PostQuantum != "no" || links[0].MeetsLevel == nil || *links[0].MeetsLevel || links[0].Requested != "PQ" || links[0].Negotiated != "X25519" {
		t.Fatalf("a device below the requested level shows the request and the result apart: %+v", links[0])
	}
	if links[1].PostQuantum != "yes" || links[1].MeetsLevel == nil || !*links[1].MeetsLevel {
		t.Fatalf("hybrid device link: %+v", links[1])
	}
	if links[2].Evidence != "unknown" || links[2].PostQuantum != "unknown" || links[2].MeetsLevel != nil {
		t.Fatalf("nothing is claimed about a session that is not up: %+v", links[2])
	}
	identities := byID["node_identity"]
	if identities[0].PostQuantum != "no" || identities[1].PostQuantum != "yes" || identities[2].PostQuantum != "unknown" {
		t.Fatalf("identity per device: %+v", identities)
	}

	// A member sees their own connection and nothing gateway-wide.
	member := auth.Principal{UserID: "u2", Username: "member", Role: auth.RoleMember}
	byID, _ = boundariesOf(t, api, hybrid, &member)
	for _, id := range []string{"gateway_node", "node_identity", "gateway_update", "node_update", "kcp"} {
		if len(byID[id]) != 0 {
			t.Fatalf("a member was shown %s", id)
		}
	}
	if len(byID["browser_gateway"]) != 1 || len(byID["call_media"]) != 1 {
		t.Fatal("a member still sees their own connection")
	}
}

func TestThroughTailscaleServeTheBrowsersHandshakeIsUnknownAndTheLocalHopIsLabelled(t *testing.T) {
	api := &API{}
	byID, body := boundariesOf(t, api, map[string]string{
		edgeHeader: edgeDirect, edgeTLSProtocol: "TLSv1.3", edgeTLSGroup: "X25519MLKEM768", edgeTLSCipher: "TLS_AES_256_GCM_SHA384",
		"Tailscale-User-Login": "someone@example.invalid",
	}, nil)
	if got := byID["browser_gateway"][0]; got.PostQuantum != "unknown" || got.Evidence != "unknown" {
		t.Fatalf("the hop nginx saw must not be presented as the browser's: %+v", got)
	}
	if got := byID["tailscale"][0]; got.Evidence != "reported" || got.PostQuantum != "no" {
		t.Fatalf("tailscale: %+v", got)
	}
	if got := byID["tailscale_gateway"][0]; got.Evidence != "observed" || got.PostQuantum != "yes" {
		t.Fatalf("the local hop is what was observed: %+v", got)
	}
	var scope string
	_ = json.Unmarshal(body["transport_scope"], &scope)
	if scope != "proxy_hop" || body["proxy"] == nil {
		t.Fatalf("the classic rows are labelled as describing the proxy hop: %s", body["transport_scope"])
	}
	// The login name in the header is evidence only; it is never echoed.
	raw, _ := json.Marshal(body)
	if string(raw) != "" && containsAny(string(raw), "someone@example.invalid") {
		t.Fatal("the posture echoed a Tailscale login")
	}
	// Plain HTTP and Cloudflare.
	byID, _ = boundariesOf(t, api, map[string]string{edgeHeader: edgeDirect}, nil)
	if got := byID["browser_gateway"][0]; got.PostQuantum != "no" || got.Protection != "none (HTTP)" {
		t.Fatalf("plain HTTP: %+v", got)
	}
	byID, _ = boundariesOf(t, api, map[string]string{edgeHeader: edgeCloudflare}, nil)
	if got := byID["browser_gateway"][0]; got.PostQuantum != "unknown" {
		t.Fatalf("through Cloudflare: %+v", got)
	}
	byID, _ = boundariesOf(t, api, nil, nil)
	if got := byID["browser_gateway"][0]; got.PostQuantum != "unknown" || got.Evidence != "unknown" {
		t.Fatalf("no edge headers: %+v", got)
	}
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if len(needle) > 0 && len(text) >= len(needle) {
			for index := 0; index+len(needle) <= len(text); index++ {
				if text[index:index+len(needle)] == needle {
					return true
				}
			}
		}
	}
	return false
}
