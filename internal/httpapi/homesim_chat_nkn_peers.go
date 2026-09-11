package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Peer notes for the nMobile chat: an NKN address is 60+ characters of hex and
// tells you nothing about who is on the other end. This keeps a local alias and
// an optional link to a contact record for each address, so conversations can
// be shown by name.
//
// The link is stored here rather than as a column on the contact, because a
// contact row is synced and backed up as the user's address book while this is
// gateway-local wiring; keeping it out of the contact schema also means no
// database migration and no change to the backup format.

const (
	nknChatAliasMaxLength = 64
	nknChatPeersMax       = 2000
)

type nknChatPeerNote struct {
	Address   string `json:"address"`
	Alias     string `json:"alias,omitempty"`
	ContactID string `json:"contact_id,omitempty"`
	UpdatedAt int64  `json:"updated_at"`
}

type nknChatPeerStore struct {
	mu sync.Mutex
}

var nknChatPeers nknChatPeerStore

func (api *API) nknChatPeersPath() (string, bool) {
	dir, ok := api.nknChatDirectory()
	if !ok {
		return "", false
	}
	return filepath.Join(dir, "peers.json"), true
}

// loadNKNChatPeers reads the note file. A missing file is not an error: it just
// means nothing has been named yet.
func (api *API) loadNKNChatPeers() (map[string]nknChatPeerNote, error) {
	notes := map[string]nknChatPeerNote{}
	path, ok := api.nknChatPeersPath()
	if !ok {
		return notes, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return notes, nil
		}
		return notes, err
	}
	var stored []nknChatPeerNote
	if json.Unmarshal(raw, &stored) != nil {
		// A corrupt file must not take the chat page down with it, and must not
		// be silently overwritten either: report it and leave it on disk.
		return notes, errNKNChatPeersCorrupt
	}
	for _, note := range stored {
		if note.Address == "" {
			continue
		}
		notes[note.Address] = note
	}
	return notes, nil
}

var errNKNChatPeersCorrupt = &nknChatPeersError{"备注文件已损坏，未做任何修改"}

type nknChatPeersError struct{ message string }

func (e *nknChatPeersError) Error() string { return e.message }

func (api *API) saveNKNChatPeers(notes map[string]nknChatPeerNote) error {
	path, ok := api.nknChatPeersPath()
	if !ok {
		return &nknChatPeersError{"备注存储不可用"}
	}
	ordered := make([]nknChatPeerNote, 0, len(notes))
	for _, note := range notes {
		ordered = append(ordered, note)
	}
	sort.Slice(ordered, func(a, b int) bool { return ordered[a].Address < ordered[b].Address })
	return writeHomeSIMJSON(path, ordered)
}

func (api *API) nknChatPeersHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		api.nknChatPeersList(w)
	case http.MethodPost:
		api.nknChatPeersUpsert(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, 405, "method_not_allowed", "仅支持读取与保存", "")
	}
}

func (api *API) nknChatPeersList(w http.ResponseWriter) {
	nknChatPeers.mu.Lock()
	notes, err := api.loadNKNChatPeers()
	nknChatPeers.mu.Unlock()
	if err != nil {
		writeError(w, 500, "peer_notes_unavailable", err.Error(), "")
		return
	}
	ordered := make([]nknChatPeerNote, 0, len(notes))
	for _, note := range notes {
		ordered = append(ordered, note)
	}
	sort.Slice(ordered, func(a, b int) bool { return ordered[a].Address < ordered[b].Address })
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"peers": ordered})
}

func (api *API) nknChatPeersUpsert(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Address   string `json:"address"`
		Alias     string `json:"alias"`
		ContactID string `json:"contact_id"`
	}
	if !decodeJSONBody(w, r, &input) {
		return
	}
	input.Address = strings.TrimSpace(input.Address)
	input.Alias = strings.TrimSpace(input.Alias)
	input.ContactID = strings.TrimSpace(input.ContactID)
	if input.Address == "" || !strings.Contains(input.Address, ".") || len(input.Address) > 200 {
		writeError(w, 400, "invalid_peer", "NKN 地址无效", "address")
		return
	}
	if len([]rune(input.Alias)) > nknChatAliasMaxLength {
		writeError(w, 400, "invalid_alias", "备注过长", "alias")
		return
	}
	// A contact id that no longer resolves would render as a conversation with
	// no name at all, so it is rejected here rather than stored and ignored.
	if input.ContactID != "" {
		if _, err := api.repository.Contact(r.Context(), input.ContactID); err != nil {
			writeError(w, 400, "unknown_contact", "联系人不存在或已被删除", "contact_id")
			return
		}
	}

	nknChatPeers.mu.Lock()
	defer nknChatPeers.mu.Unlock()
	notes, err := api.loadNKNChatPeers()
	if err != nil {
		writeError(w, 500, "peer_notes_unavailable", err.Error(), "")
		return
	}
	if input.Alias == "" && input.ContactID == "" {
		delete(notes, input.Address)
	} else {
		if _, exists := notes[input.Address]; !exists && len(notes) >= nknChatPeersMax {
			writeError(w, 409, "peer_notes_full", "备注数量已达上限", "")
			return
		}
		notes[input.Address] = nknChatPeerNote{
			Address:   input.Address,
			Alias:     input.Alias,
			ContactID: input.ContactID,
			UpdatedAt: time.Now().Unix(),
		}
	}
	if err := api.saveNKNChatPeers(notes); err != nil {
		writeError(w, 500, "peer_notes_write_failed", "备注保存失败", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "peer": notes[input.Address]})
}
