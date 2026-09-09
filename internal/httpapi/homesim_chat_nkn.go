package httpapi

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	nkn "github.com/nknorg/nkn-sdk-go"
)

// NKN text chat ("nMobile" entry): messages between this gateway's NKN
// identity and peer NKN addresses. Everything is stored locally on the NAS
// as JSONL; nothing leaves the box except the encrypted NKN transport itself.
// The chat protocol is a plain JSON envelope over the existing encrypted
// NKN session; unknown message types are ignored by the receiver.

const (
	nknChatType        = "homesim.chat.v1"
	nknChatAckType     = "homesim.chat.ack.v1"
	nknChatMaxText     = 2000
	nknChatMaxPayload  = 4096
	nknChatHistoryRead = 500
)

type nknChatEnvelope struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	ID   string `json:"id,omitempty"`
}

type nknChatMessage struct {
	Ts     int64  `json:"ts"`
	Dir    string `json:"dir"` // "in" or "out"
	Peer   string `json:"peer"`
	Text   string `json:"text"`
	ID     string `json:"id,omitempty"`
	Status string `json:"status,omitempty"` // out only: "sent" -> "delivered"
}

type nknChatStore struct {
	mu  sync.Mutex
	mem []nknChatMessage
}

var nknChatMessages nknChatStore

func (api *API) nknChatDirectory() (string, bool) {
	dir, ok := api.homeSIMDataDirectory()
	if !ok {
		return "", false
	}
	path := filepath.Join(dir, "nmobile")
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", false
	}
	return path, true
}

func appendNKNChatMessage(message nknChatMessage) {
	nknChatMessages.mu.Lock()
	nknChatMessages.mem = append(nknChatMessages.mem, message)
	if len(nknChatMessages.mem) > nknChatHistoryRead {
		nknChatMessages.mem = nknChatMessages.mem[len(nknChatMessages.mem)-nknChatHistoryRead:]
	}
	nknChatMessages.mu.Unlock()

	dir, ok := apiChatDir()
	if !ok {
		return
	}
	data, err := json.Marshal(message)
	if err != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(dir, "messages.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(append(data, '\n'))
}

// apiChatDir is a package-level trampoline because appendNKNChatMessage is
// called from receive paths that carry no *API receiver.
var apiChatDir = func() (string, bool) { return "", false }

func (api *API) initNKNChatStore() {
	apiChatDir = api.nknChatDirectory
	nknChatMessages.mu.Lock()
	defer nknChatMessages.mu.Unlock()
	nknChatMessages.mem = nil
	dir, ok := api.nknChatDirectory()
	if !ok {
		return
	}
	file, err := os.Open(filepath.Join(dir, "messages.jsonl"))
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var message nknChatMessage
		if json.Unmarshal([]byte(line), &message) == nil && message.Text != "" {
			nknChatMessages.mem = append(nknChatMessages.mem, message)
		}
	}
	if len(nknChatMessages.mem) > nknChatHistoryRead {
		nknChatMessages.mem = nknChatMessages.mem[len(nknChatMessages.mem)-nknChatHistoryRead:]
	}
}

// receiveNKNChat handles one inbound NKN message for the chat feature and
// reports whether it was consumed as chat traffic.
func (api *API) receiveNKNChat(message *nkn.Message) bool {
	if message == nil || !message.Encrypted || len(message.Data) == 0 || len(message.Data) > nknChatMaxPayload {
		return false
	}
	var envelope nknChatEnvelope
	if json.Unmarshal(message.Data, &envelope) != nil {
		return false
	}
	switch envelope.Type {
	case nknChatAckType:
		// Delivery receipts from a peer running NasSimHub: flip matching out
		// messages to "delivered" so the UI can show the double check mark.
		if len(envelope.ID) > 0 {
			markNKNChatDelivered(envelope.ID)
		}
		return true
	case nknChatType:
	default:
		return false
	}
	text := strings.TrimSpace(envelope.Text)
	if text == "" || len(text) > nknChatMaxText {
		return true
	}
	id := envelope.ID
	if id == "" {
		id = nknChatNewID()
	}
	appendNKNChatMessage(nknChatMessage{Ts: time.Now().Unix(), Dir: "in", Peer: message.Src, Text: text, ID: id})
	// Best-effort delivery receipt back to the sender so their check marks
	// can turn double. Runs async; failure just means the peer keeps the
	// single tick, which is honest.
	go func(src, ackID string) {
		seed, err := api.managedNKNSeed()
		if err != nil {
			return
		}
		entry, err := api.homeSIMNKNClient(seed)
		if err != nil || entry.client == nil || entry.client.IsClosed() {
			return
		}
		payload, _ := json.Marshal(nknChatEnvelope{Type: nknChatAckType, ID: ackID})
		_, _ = entry.client.Send(nkn.NewStringArray(src), payload, &nkn.MessageConfig{NoReply: true, MaxHoldingSeconds: 600})
	}(message.Src, id)
	return true
}

// markNKNChatDelivered flips an out message to delivered in memory and on
// disk (the JSONL file is small, so a full rewrite is fine).
func markNKNChatDelivered(id string) {
	nknChatMessages.mu.Lock()
	found := false
	for index := range nknChatMessages.mem {
		if nknChatMessages.mem[index].ID == id && nknChatMessages.mem[index].Dir == "out" {
			nknChatMessages.mem[index].Status = "delivered"
			found = true
		}
	}
	var snapshot []nknChatMessage
	if found {
		snapshot = append(snapshot, nknChatMessages.mem...)
	}
	nknChatMessages.mu.Unlock()
	if !found {
		return
	}
	if dir, ok := apiChatDir(); ok {
		var builder strings.Builder
		for _, message := range snapshot {
			data, err := json.Marshal(message)
			if err != nil {
				continue
			}
			builder.Write(data)
			builder.WriteByte('\n')
		}
		target := filepath.Join(dir, "messages.jsonl")
		tmp := target + ".tmp"
		if os.WriteFile(tmp, []byte(builder.String()), 0o600) == nil {
			_ = os.Rename(tmp, target)
		}
	}
}

func (api *API) nknChatMessagesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, 405, "method_not_allowed", "仅支持读取", "")
		return
	}
	after := int64(0)
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, _ = strconv.ParseInt(raw, 10, 64)
	}
	nknChatMessages.mu.Lock()
	defer nknChatMessages.mu.Unlock()
	result := make([]nknChatMessage, 0, len(nknChatMessages.mem))
	for _, message := range nknChatMessages.mem {
		if message.Ts > after {
			result = append(result, message)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"messages": result})
}

func (api *API) nknChatSendHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, 405, "method_not_allowed", "仅支持发送", "")
		return
	}
	var input struct {
		To   string `json:"to"`
		Text string `json:"text"`
	}
	if !decodeJSONBody(w, r, &input) {
		return
	}
	input.To = strings.TrimSpace(input.To)
	input.Text = strings.TrimSpace(input.Text)
	if input.To == "" || !strings.Contains(input.To, ".") || len(input.To) > 200 {
		writeError(w, 400, "invalid_peer", "目标 NKN 地址无效", "to")
		return
	}
	if input.Text == "" || len(input.Text) > nknChatMaxText {
		writeError(w, 400, "invalid_text", "消息内容为空或超出长度限制", "text")
		return
	}
	seed, err := api.managedNKNSeed()
	if err != nil {
		writeError(w, 409, "nkn_unavailable", "NKN 网络未连接，无法发送", "")
		return
	}
	client, err := api.homeSIMNKNClient(seed)
	if err != nil {
		writeError(w, 409, "nkn_unavailable", "NKN 网络暂不可达，请稍后重试", "")
		return
	}
	select {
	case <-client.ready:
	case <-time.After(20 * time.Second):
		writeError(w, 409, "nkn_unavailable", "NKN 连接建立超时，请稍后重试", "")
		return
	case <-r.Context().Done():
		return
	}
	if client.err != nil || client.client == nil || client.client.IsClosed() {
		writeError(w, 409, "nkn_unavailable", "NKN 连接不可用，请稍后重试", "")
		return
	}
	messageID := nknChatNewID()
	payload, _ := json.Marshal(nknChatEnvelope{Type: nknChatType, Text: input.Text, ID: messageID})
	if _, err := client.client.Send(nkn.NewStringArray(input.To), payload, &nkn.MessageConfig{NoReply: true, MaxHoldingSeconds: 3600}); err != nil {
		writeError(w, 502, "nkn_send_failed", "NKN 发送失败，请稍后重试", "")
		return
	}
	ts := time.Now().Unix()
	appendNKNChatMessage(nknChatMessage{Ts: ts, Dir: "out", Peer: input.To, Text: input.Text, ID: messageID, Status: "sent"})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": messageID, "ts": ts})
}

// nknChatNewID generates a random 16-hex-char message id for delivery receipts.
func nknChatNewID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf)
}

var _ = context.Background
