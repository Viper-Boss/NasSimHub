package httpapi

import (
	"net/http"
	"testing"
)

// Review P1-6 / audit A-11: nMobile chat has one gateway-wide NKN identity and
// no per-member owner, so a member must not read, send, clear or annotate it.
func TestNKNChatIsAdministratorOnly(t *testing.T) {
	harness := newAdvHarness(t)

	appendNKNChatMessage(nknChatMessage{Ts: 1, Dir: "in", Peer: "peer.nkn-address", Text: "for the owner", ID: "a1"})
	t.Cleanup(func() {
		nknChatMessages.mu.Lock()
		nknChatMessages.mem = nil
		nknChatMessages.mu.Unlock()
	})

	member := harness.aliceClient()
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/nkn/chat/messages", ""},
		{http.MethodPost, "/api/v1/nkn/chat/clear", "{}"},
		{http.MethodPost, "/api/v1/nkn/chat/send", `{"to":"peer.nkn-address","text":"hi"}`},
		{http.MethodGet, "/api/v1/nkn/chat/peers", ""},
		{http.MethodPost, "/api/v1/nkn/chat/peers", `{"address":"peer.nkn-address","alias":"x"}`},
	} {
		response := member.send(request.method, request.path, request.body)
		advRequireStatus(t, response, http.StatusForbidden, "admin_required")
	}

	nknChatMessages.mu.Lock()
	kept := len(nknChatMessages.mem)
	nknChatMessages.mu.Unlock()
	if kept != 1 {
		t.Fatalf("a member's clear request removed history: %d messages left, want 1", kept)
	}

	response := harness.admin().get("/api/v1/nkn/chat/messages")
	advRequireStatus(t, response, http.StatusOK, "")
}
