package diagnostics

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	DefaultLogCapacity = 2000
	subscriberCapacity = 128
)

type LogEntry struct {
	ID        uint64         `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Level     string         `json:"level"`
	Source    string         `json:"source,omitempty"`
	Component string         `json:"component"`
	Caller    string         `json:"caller,omitempty"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
}

type LogWindow struct {
	Entries   []LogEntry `json:"entries"`
	OldestID  uint64     `json:"oldest_id"`
	NewestID  uint64     `json:"newest_id"`
	Truncated bool       `json:"truncated"`
}

type LogSource interface {
	Snapshot(after uint64) LogWindow
	Subscribe(after uint64) (LogWindow, <-chan LogEntry, func())
}

type LogBuffer struct {
	mu          sync.Mutex
	capacity    int
	nextID      uint64
	entries     []LogEntry
	nextClient  uint64
	subscribers map[uint64]chan LogEntry
}

func NewLogBuffer(capacity int) *LogBuffer {
	if capacity <= 0 {
		capacity = DefaultLogCapacity
	}
	return &LogBuffer{
		capacity:    capacity,
		entries:     make([]LogEntry, 0, capacity),
		subscribers: make(map[uint64]chan LogEntry),
	}
}

func (b *LogBuffer) Handler(next slog.Handler) slog.Handler {
	if next == nil {
		next = slog.NewTextHandler(discardWriter{}, nil)
	}
	return &logHandler{next: next, buffer: b}
}

func (b *LogBuffer) Snapshot(after uint64) LogWindow {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.windowLocked(after)
}

func (b *LogBuffer) Subscribe(after uint64) (LogWindow, <-chan LogEntry, func()) {
	b.mu.Lock()
	b.nextClient++
	clientID := b.nextClient
	updates := make(chan LogEntry, subscriberCapacity)
	b.subscribers[clientID] = updates
	window := b.windowLocked(after)
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			if current, ok := b.subscribers[clientID]; ok {
				delete(b.subscribers, clientID)
				close(current)
			}
			b.mu.Unlock()
		})
	}
	return window, updates, cancel
}

// AppendExternal imports one already-structured entry from another trusted
// ModemDeck process. The local buffer always assigns the public cursor so a
// hardware-agent restart cannot make browser SSE IDs move backwards.
func (b *LogBuffer) AppendExternal(entry LogEntry) {
	entry.Source = normalizedValue(entry.Source, "external")
	entry.Component = normalizedValue(entry.Component, "runtime")
	entry.Level = normalizedLevel(entry.Level)
	entry.Message = ScrubSecrets(strings.TrimSpace(entry.Message))
	entry.Fields = sanitizeFields(entry.Fields)
	b.append(entry)
}

func (b *LogBuffer) append(entry LogEntry) {
	b.mu.Lock()
	b.nextID++
	entry.ID = b.nextID
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	} else {
		entry.Timestamp = entry.Timestamp.UTC()
	}
	if len(b.entries) == b.capacity {
		copy(b.entries, b.entries[1:])
		b.entries[len(b.entries)-1] = entry
	} else {
		b.entries = append(b.entries, entry)
	}
	for id, subscriber := range b.subscribers {
		select {
		case subscriber <- cloneLogEntry(entry):
		default:
			delete(b.subscribers, id)
			close(subscriber)
		}
	}
	b.mu.Unlock()
}

func (b *LogBuffer) windowLocked(after uint64) LogWindow {
	window := LogWindow{Entries: []LogEntry{}}
	if len(b.entries) == 0 {
		return window
	}
	window.OldestID = b.entries[0].ID
	window.NewestID = b.entries[len(b.entries)-1].ID
	if after > window.NewestID {
		// A future cursor normally means the upstream process restarted and its
		// in-memory sequence began again. Replay the retained window explicitly.
		window.Truncated = true
		after = 0
	} else {
		window.Truncated = after > 0 && after+1 < window.OldestID
	}
	for _, entry := range b.entries {
		if entry.ID > after {
			window.Entries = append(window.Entries, cloneLogEntry(entry))
		}
	}
	return window
}

type logHandler struct {
	next   slog.Handler
	buffer *LogBuffer
	attrs  []boundAttr
	groups []string
}

type boundAttr struct {
	groups []string
	attr   slog.Attr
}

func (h *logHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// Handle redacts once and feeds both sinks from the result.
//
// The record used to be redacted for the in-memory buffer only and then
// handed to the next handler untouched, so the process log (stdout, i.e.
// `docker logs`) carried in clear exactly what the diagnostics page had
// hidden. That log is what gets pasted into a bug report. The forwarded record
// is therefore rebuilt from the redacted attributes, and WithAttrs below does
// the same for bound ones.
func (h *logHandler) Handle(ctx context.Context, record slog.Record) error {
	fields := make(map[string]any)
	for _, bound := range h.attrs {
		addAttr(fields, bound.groups, bound.attr)
	}
	message := ScrubSecrets(record.Message)
	forwarded := slog.NewRecord(record.Time, record.Level, message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		addAttr(fields, h.groups, attr)
		forwarded.AddAttrs(redactAttr(h.groups, attr))
		return true
	})
	component := "app"
	if value, ok := fields["component"].(string); ok {
		value = strings.TrimSpace(value)
		if value != "" {
			component = value
		}
	}
	delete(fields, "component")
	source := "application"
	if value, ok := fields["source"].(string); ok {
		value = strings.TrimSpace(value)
		if value != "" {
			source = value
		}
	}
	delete(fields, "source")
	h.buffer.append(LogEntry{
		Timestamp: record.Time,
		Level:     strings.ToLower(record.Level.String()),
		Source:    source,
		Component: component,
		Caller:    caller(record.PC),
		Message:   message,
		Fields:    fields,
	})
	return h.next.Handle(ctx, forwarded)
}

func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	combined := make([]boundAttr, 0, len(h.attrs)+len(attrs))
	combined = append(combined, h.attrs...)
	for _, attr := range attrs {
		combined = append(combined, boundAttr{
			groups: append([]string(nil), h.groups...),
			attr:   attr,
		})
	}
	redacted := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		redacted = append(redacted, redactAttr(h.groups, attr))
	}
	return &logHandler{
		next:   h.next.WithAttrs(redacted),
		buffer: h.buffer,
		attrs:  combined,
		groups: append([]string(nil), h.groups...),
	}
}

func (h *logHandler) WithGroup(name string) slog.Handler {
	name = strings.TrimSpace(name)
	if name == "" {
		return h
	}
	groups := append(append([]string(nil), h.groups...), name)
	return &logHandler{
		next:   h.next.WithGroup(name),
		buffer: h.buffer,
		attrs:  append([]boundAttr(nil), h.attrs...),
		groups: groups,
	}
}

func addAttr(fields map[string]any, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	if attr.Value.Kind() == slog.KindGroup {
		group := groups
		if strings.TrimSpace(attr.Key) != "" {
			group = append(append([]string(nil), groups...), attr.Key)
		}
		for _, nested := range attr.Value.Group() {
			addAttr(fields, group, nested)
		}
		return
	}
	key := strings.Trim(strings.Join(append(append([]string(nil), groups...), attr.Key), "."), ".")
	if key == "" {
		return
	}
	if sensitiveKey(key) {
		fields[key] = "[redacted]"
		return
	}
	fields[key] = logValue(attr.Value)
}

// redactAttr is addAttr for the forwarded record: the same decisions, kept in
// slog's own shape so the next handler still sees groups as groups.
func redactAttr(groups []string, attr slog.Attr) slog.Attr {
	attr.Value = attr.Value.Resolve()
	if attr.Value.Kind() == slog.KindGroup {
		group := groups
		if strings.TrimSpace(attr.Key) != "" {
			group = append(append([]string(nil), groups...), attr.Key)
		}
		members := attr.Value.Group()
		redacted := make([]slog.Attr, 0, len(members))
		for _, nested := range members {
			redacted = append(redacted, redactAttr(group, nested))
		}
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(redacted...)}
	}
	key := strings.Trim(strings.Join(append(append([]string(nil), groups...), attr.Key), "."), ".")
	if sensitiveKey(key) {
		return slog.String(attr.Key, "[redacted]")
	}
	switch attr.Value.Kind() {
	case slog.KindString:
		return slog.String(attr.Key, ScrubSecrets(attr.Value.String()))
	case slog.KindAny:
		// An error is rendered to text here, so that what is scrubbed is what
		// is written. (A JSON handler prints an error as its text anyway, so
		// the shape of the line does not change.) Other values pass through
		// in their own type, as before.
		if err, ok := attr.Value.Any().(error); ok {
			return slog.String(attr.Key, ScrubSecrets(err.Error()))
		}
	}
	return attr
}

// Secrets that arrive inside a VALUE.
//
// Redaction by key name cannot see these: the key is "error" and the value is
// whatever a library put in its error text. net/http puts the request URL
// there, and for a push channel the URL is the credential -- a Bark device
// key, a ServerChan send key and a Telegram bot token are path segments, a
// DingTalk or WeCom token is a query parameter. A failed push therefore wrote
// the credential into the log every time the network hiccuped.
//
// ScrubSecrets is deliberately narrow, so that ordinary diagnostics survive
// it: the scheme, host and short path segments of a URL are kept ("which
// service failed" stays answerable); what goes is userinfo, every query value,
// the fragment, and any path segment long enough to be a key.
var (
	urlInText        = regexp.MustCompile(`(?i)\b(?:https?|wss?)://[^\s"'<>\\]+`)
	telegramBotToken = regexp.MustCompile(`\b\d{6,12}:[A-Za-z0-9_-]{30,}`)
	// Sixteen characters and up, so that prose such as "Bearer authentication
	// failed" is left alone.
	bearerCredential = regexp.MustCompile(`(?i)\b(Bearer)\s+[A-Za-z0-9._~+/=-]{16,}`)
)

// A path segment this long is treated as a key. Bark device keys are 22
// characters, ServerChan send keys longer, webhook identifiers are UUIDs; the
// names of the service's own endpoints ("sendMessage", "push", "v2") are
// shorter and survive.
const maxOpaquePathPart = 16

// ScrubSecrets removes credentials that are embedded in free text: URLs with
// secret path segments or query strings, Telegram bot tokens, and
// Authorization header values. Text with none of those is returned unchanged.
func ScrubSecrets(text string) string {
	if text == "" {
		return text
	}
	text = urlInText.ReplaceAllStringFunc(text, scrubURL)
	text = telegramBotToken.ReplaceAllString(text, "[redacted]")
	text = bearerCredential.ReplaceAllString(text, "$1 [redacted]")
	return text
}

func scrubURL(raw string) string {
	// Punctuation that ends a sentence or closes a quote is not part of the
	// URL; keep it outside so the surrounding text reads the same.
	trimmed := strings.TrimRight(raw, ".,;:)]}")
	tail := raw[len(trimmed):]
	// ...except the bracket that closes this function's own marker, so that
	// scrubbing already-scrubbed text changes nothing.
	if strings.HasSuffix(trimmed, "[redacted") && strings.HasPrefix(tail, "]") {
		trimmed, tail = trimmed+"]", tail[1:]
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		// Unparseable, so nothing in it can be vouched for.
		if scheme, _, found := strings.Cut(trimmed, "://"); found {
			return scheme + "://[redacted]" + tail
		}
		return "[redacted]" + tail
	}
	var result strings.Builder
	result.WriteString(parsed.Scheme)
	result.WriteString("://")
	result.WriteString(parsed.Host)
	for index, segment := range strings.Split(parsed.EscapedPath(), "/") {
		if index > 0 {
			result.WriteByte('/')
		}
		if opaquePathSegment(segment) {
			result.WriteString("[redacted]")
			continue
		}
		result.WriteString(segment)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		result.WriteString("?[redacted]")
	}
	return result.String() + tail
}

func opaquePathSegment(segment string) bool {
	if len(segment) >= maxOpaquePathPart {
		return true
	}
	// Telegram's "/bot<id>:<token>/method" survives URL escaping in several
	// shapes; anything that starts like one is treated as one.
	lowered := strings.ToLower(segment)
	return strings.HasPrefix(lowered, "bot") && len(segment) > 3 &&
		segment[3] >= '0' && segment[3] <= '9'
}

func logValue(value slog.Value) any {
	switch value.Kind() {
	case slog.KindString:
		return ScrubSecrets(value.String())
	case slog.KindBool:
		return value.Bool()
	case slog.KindInt64:
		return value.Int64()
	case slog.KindUint64:
		return value.Uint64()
	case slog.KindFloat64:
		return value.Float64()
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindTime:
		return value.Time().UTC()
	case slog.KindAny:
		if err, ok := value.Any().(error); ok {
			return ScrubSecrets(err.Error())
		}
		return ScrubSecrets(fmt.Sprint(value.Any()))
	default:
		return value.String()
	}
}

func sensitiveKey(key string) bool {
	key = strings.ToLower(key)
	for _, segment := range strings.FieldsFunc(key, func(r rune) bool {
		return r == '.' || r == '_' || r == '-'
	}) {
		switch segment {
		case "authorization", "cookie", "credential", "password", "secret", "token",
			"number", "phone", "peer", "recipient", "sender", "content", "text",
			"imsi", "iccid", "imei", "sdp", "digits",
			// r128: credentials the list above had no word for.
			"psk", "passphrase", "puk", "otp", "csrf", "apikey":
			return true
		}
	}
	return false
}

func sanitizeFields(fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	sanitized := make(map[string]any, len(fields))
	for key, value := range fields {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if sensitiveKey(key) {
			sanitized[key] = "[redacted]"
			continue
		}
		if text, ok := value.(string); ok {
			value = ScrubSecrets(text)
		}
		sanitized[key] = value
	}
	if len(sanitized) == 0 {
		return nil
	}
	return sanitized
}

func normalizedValue(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func normalizedLevel(value string) string {
	switch value = strings.ToLower(strings.TrimSpace(value)); value {
	case "debug", "info", "warn", "error":
		return value
	default:
		return "info"
	}
}

func caller(pc uintptr) string {
	if pc == 0 {
		return ""
	}
	frame, _ := runtime.CallersFrames([]uintptr{pc}).Next()
	if frame.File == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", filepath.Base(frame.File), frame.Line)
}

func cloneLogEntry(entry LogEntry) LogEntry {
	cloned := entry
	if entry.Fields != nil {
		cloned.Fields = make(map[string]any, len(entry.Fields))
		for key, value := range entry.Fields {
			cloned.Fields[key] = value
		}
	}
	return cloned
}

type discardWriter struct{}

func (discardWriter) Write(value []byte) (int, error) {
	return len(value), nil
}
