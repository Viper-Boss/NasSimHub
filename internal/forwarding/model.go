// SPDX-License-Identifier: AGPL-3.0-only
// Package forwarding implements NAS-native notifications. It is independent of
// Android SmsForwarder's runtime and never executes shell commands or URL schemes.
package forwarding

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"text/template"
	"time"
	_ "time/tzdata" // Keep IANA quiet-hour zones available in minimal NAS images.
)

var ErrConflict = errors.New("forwarding_revision_conflict")
var ErrInvalid = errors.New("invalid_forwarding_configuration")
var ErrUnavailable = errors.New("forwarding_unavailable")
var ErrDenied = errors.New("forwarding_denied")

const MaxAge = 24 * time.Hour
const MaxAttempts = 5

type Secret struct {
	Env        string `json:"env,omitempty"`
	Value      string `json:"value,omitempty"` // write-only input; never persisted
	Set        bool   `json:"set,omitempty"`
	Nonce      []byte `json:"nonce,omitempty"` // private database representation only
	Ciphertext []byte `json:"ciphertext,omitempty"`
}
type Channel struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	Enabled bool              `json:"enabled"`
	Params  map[string]string `json:"params"`
	Secrets map[string]Secret `json:"secrets"`
}
type Condition struct {
	Op       string      `json:"op"`
	Field    string      `json:"field,omitempty"`
	Value    string      `json:"value,omitempty"`
	Children []Condition `json:"children,omitempty"`
}
type QuietHours struct {
	Enabled  bool   `json:"enabled"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Timezone string `json:"timezone"`
	Days     []int  `json:"days"` // Sunday=0; empty means every day
}
type Rule struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Enabled    bool       `json:"enabled"`
	Events     []string   `json:"events"`
	LineIDs    []string   `json:"line_ids"`
	Condition  Condition  `json:"condition"`
	ChannelIDs []string   `json:"channel_ids"`
	Logic      string     `json:"logic"` // all, until_success, until_fail
	TemplateID string     `json:"template_id"`
	Stop       bool       `json:"stop"`
	Quiet      QuietHours `json:"quiet"`
}
type Template struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Title string `json:"title"`
	Body  string `json:"body"`
}
type Config struct {
	Revision  int64      `json:"revision"`
	OwnerID   string     `json:"owner_id,omitempty"`
	Enabled   bool       `json:"enabled"`
	Channels  []Channel  `json:"channels"`
	Rules     []Rule     `json:"rules"`
	Templates []Template `json:"templates"`
}
type Event struct {
	Seq               int64     `json:"seq"`
	ID                string    `json:"id"`
	Type              string    `json:"type"`
	ResourceID        string    `json:"resource_id"`
	LineID            string    `json:"line_id"`
	Peer              string    `json:"peer"`
	Body              string    `json:"body"`
	OccurredAt        time.Time `json:"occurred_at"`
	EndpointID        string    `json:"endpoint_id"`
	EndpointMessageID string    `json:"endpoint_message_id"`
}
type Rendered struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}
type ChannelKind struct {
	Type    string   `json:"type"`
	Params  []string `json:"params"`
	Secrets []string `json:"secrets"`
}

// Fifteen NAS channel families. socket supports TCP, UDP and MQTT 3.1.1 QoS1.
var Catalog = []ChannelKind{
	{"dingtalk", []string{}, []string{"url", "signing_key"}},
	{"dingtalk_app", []string{"agent_id", "to"}, []string{"app_key", "app_secret"}},
	{"email", []string{"host", "port", "username", "from", "to", "tls"}, []string{"password"}},
	{"bark", []string{"server", "group", "sound", "encryption"}, []string{"key", "encryption_key"}},
	{"webhook", []string{}, []string{"url", "headers"}},
	{"wecom", []string{}, []string{"url"}},
	{"wecom_app", []string{"agent_id", "to"}, []string{"corp_id", "corp_secret"}},
	{"serverchan", []string{}, []string{"key"}},
	{"telegram", []string{"chat_id"}, []string{"token"}},
	{"sms", []string{"line_id", "to"}, []string{}},
	{"feishu", []string{}, []string{"url", "signing_key"}},
	{"feishu_app", []string{"to", "receive_id_type"}, []string{"app_id", "app_secret"}},
	{"pushplus", []string{"topic"}, []string{"token"}},
	{"gotify", []string{"server", "priority"}, []string{"token"}},
	{"socket", []string{"protocol", "host", "port", "tls", "topic", "client_id", "username"}, []string{"password"}},
}

func DefaultConfig() Config {
	return Config{Revision: 1, Channels: []Channel{}, Rules: []Rule{}, Templates: []Template{{ID: "default", Name: "Default", Title: "NasSimHub · {{.Event}} · {{.Peer}}", Body: "{{.Body}}\n{{.LineID}} · {{.Time}}"}}}
}
func (c Config) Public() Config {
	out := c
	out.Rules = append([]Rule{}, c.Rules...)
	for i := range out.Rules {
		out.Rules[i].LineIDs = append([]string{}, out.Rules[i].LineIDs...)
		out.Rules[i].Quiet.Days = append([]int{}, out.Rules[i].Quiet.Days...)
	}
	out.Channels = make([]Channel, len(c.Channels))
	for i, ch := range c.Channels {
		out.Channels[i] = ch
		if out.Channels[i].Params == nil {
			out.Channels[i].Params = map[string]string{}
		}
		out.Channels[i].Secrets = map[string]Secret{}
		for k, s := range ch.Secrets {
			out.Channels[i].Secrets[k] = Secret{Env: s.Env, Set: len(s.Ciphertext) > 0}
		}
	}
	return out
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,64}$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func (c Config) Validate() error {
	if c.Revision < 1 || len(c.Channels) > 100 || len(c.Rules) > 64 || len(c.Templates) == 0 || len(c.Templates) > 64 {
		return ErrInvalid
	}
	channels := map[string]bool{}
	templates := map[string]bool{}
	for _, ch := range c.Channels {
		if !identifier.MatchString(ch.ID) || channels[ch.ID] || len(ch.Name) > 128 {
			return ErrInvalid
		}
		channels[ch.ID] = true
		if !validateChannelOptions(ch) {
			return ErrInvalid
		}
		kind, ok := Kind(ch.Type)
		if !ok {
			return ErrInvalid
		}
		for k, v := range ch.Params {
			if !slices.Contains(kind.Params, k) || len(v) > 4096 || strings.ContainsAny(v, "\r\n") {
				return ErrInvalid
			}
		}
		for k, s := range ch.Secrets {
			if !slices.Contains(kind.Secrets, k) || len(s.Value) > 8192 || len(s.Ciphertext) > 16384 || len(s.Nonce) > 64 || s.Env != "" && !envName.MatchString(s.Env) || s.Env != "" && (s.Value != "" || len(s.Ciphertext) > 0) {
				return ErrInvalid
			}
		}
	}
	for _, t := range c.Templates {
		if !identifier.MatchString(t.ID) || templates[t.ID] || len(t.Name) > 128 || len(t.Title) > 4096 || len(t.Body) > 16384 {
			return ErrInvalid
		}
		templates[t.ID] = true
		if _, err := render(t, Event{}); err != nil {
			return ErrInvalid
		}
	}
	ids := map[string]bool{}
	for _, r := range c.Rules {
		if !identifier.MatchString(r.ID) || ids[r.ID] || len(r.Name) > 128 || !templates[r.TemplateID] || len(r.ChannelIDs) == 0 || len(r.ChannelIDs) > 100 || len(r.LineIDs) > 128 || len(r.Events) == 0 || len(r.Events) > 3 {
			return ErrInvalid
		}
		ids[r.ID] = true
		if r.Logic != "all" && r.Logic != "until_success" && r.Logic != "until_fail" {
			return ErrInvalid
		}
		seen := map[string]bool{}
		for _, id := range r.ChannelIDs {
			if !channels[id] || seen[id] {
				return ErrInvalid
			}
			seen[id] = true
		}
		for _, event := range r.Events {
			if event != "incoming_sms" && event != "incoming_call" && event != "missed_call" {
				return ErrInvalid
			}
		}
		for _, line := range r.LineIDs {
			if line == "" || len(line) > 128 {
				return ErrInvalid
			}
		}
		budget := 128
		if !validCondition(r.Condition, 0, &budget) {
			return ErrInvalid
		}
		if r.Quiet.Enabled {
			_, e1 := time.Parse("15:04", r.Quiet.Start)
			_, e2 := time.Parse("15:04", r.Quiet.End)
			_, e3 := time.LoadLocation(r.Quiet.Timezone)
			if e1 != nil || e2 != nil || e3 != nil || r.Quiet.Start == r.Quiet.End {
				return ErrInvalid
			}
			for _, day := range r.Quiet.Days {
				if day < 0 || day > 6 {
					return ErrInvalid
				}
			}
		}
	}
	return nil
}
func Kind(name string) (ChannelKind, bool) {
	for _, k := range Catalog {
		if k.Type == name {
			return k, true
		}
	}
	return ChannelKind{}, false
}
func validCondition(c Condition, depth int, budget *int) bool {
	*budget--
	if depth > 12 || *budget < 0 || len(c.Value) > 4096 || len(c.Children) > 32 {
		return false
	}
	switch c.Op {
	case "", "always":
		return len(c.Children) == 0
	case "and", "or", "not":
		if len(c.Children) == 0 || c.Op == "not" && len(c.Children) != 1 {
			return false
		}
		for _, child := range c.Children {
			if !validCondition(child, depth+1, budget) {
				return false
			}
		}
		return true
	case "is", "notis", "contain", "notcontain", "regex":
		if len(c.Children) > 0 || !slices.Contains([]string{"peer", "body", "line_id", "event"}, c.Field) {
			return false
		}
		if c.Op == "regex" {
			_, err := regexp.Compile(c.Value)
			return err == nil
		}
		return c.Value != ""
	}
	return false
}
func (c Condition) Matches(e Event) bool {
	switch c.Op {
	case "", "always":
		return true
	case "and":
		for _, s := range c.Children {
			if !s.Matches(e) {
				return false
			}
		}
		return true
	case "or":
		for _, s := range c.Children {
			if s.Matches(e) {
				return true
			}
		}
		return false
	case "not":
		return len(c.Children) == 1 && !c.Children[0].Matches(e)
	}
	value := map[string]string{"peer": e.Peer, "body": e.Body, "line_id": e.LineID, "event": e.Type}[c.Field]
	if c.Op == "regex" {
		matched, err := regexp.MatchString(c.Value, value)
		return err == nil && matched
	}
	positive := strings.TrimPrefix(c.Op, "not")
	matched := false
	for _, or := range strings.Split(c.Value, "||") {
		all := true
		for _, and := range strings.Split(or, "&&") {
			item := strings.TrimSpace(and)
			if positive == "is" {
				all = all && value == item
			} else {
				all = all && strings.Contains(value, item)
			}
		}
		matched = matched || all
	}
	if c.Op == "notis" || c.Op == "notcontain" {
		return !matched
	}
	return matched
}
func (r Rule) Matches(e Event) bool {
	return r.Enabled && slices.Contains(r.Events, e.Type) && (len(r.LineIDs) == 0 || slices.Contains(r.LineIDs, e.LineID)) && r.Condition.Matches(e)
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 65536 {
		return 0, ErrInvalid
	}
	return b.Buffer.Write(p)
}

var templateField = regexp.MustCompile(`{{\s*\.(Peer|Body|LineID|Event|Time|ResourceID)\s*}}`)

func (b *limitedBuffer) WriteString(s string) (int, error) { return b.Write([]byte(s)) }
func render(t Template, e Event) (Rendered, error) {
	for _, text := range []string{t.Title, t.Body} {
		literal := templateField.ReplaceAllString(text, "")
		if strings.Contains(literal, "{{") || strings.Contains(literal, "}}") {
			return Rendered{}, ErrInvalid
		}
	}
	data := map[string]string{"Peer": e.Peer, "Body": e.Body, "LineID": e.LineID, "Event": e.Type, "Time": e.OccurredAt.Format(time.RFC3339), "ResourceID": e.ResourceID}
	texts := []string{t.Title, t.Body}
	result := make([]string, 2)
	for i, text := range texts {
		parsed, err := template.New("notification").Option("missingkey=error").Parse(text)
		if err != nil {
			return Rendered{}, err
		}
		var b limitedBuffer
		if err := parsed.Execute(&b, data); err != nil {
			return Rendered{}, err
		}
		result[i] = b.String()
	}
	return Rendered{result[0], result[1]}, nil
}
func (c Config) Render(rule Rule, e Event) (Rendered, error) {
	for _, t := range c.Templates {
		if t.ID == rule.TemplateID {
			return render(t, e)
		}
	}
	return Rendered{}, fmt.Errorf("%w: template", ErrInvalid)
}
func (q QuietHours) Next(now time.Time) time.Time {
	if !q.Enabled {
		return now
	}
	loc, err := time.LoadLocation(q.Timezone)
	if err != nil {
		return now.Add(MaxAge)
	}
	local := now.In(loc)
	start, _ := time.Parse("15:04", q.Start)
	end, _ := time.Parse("15:04", q.End)
	from := time.Date(local.Year(), local.Month(), local.Day(), start.Hour(), start.Minute(), 0, 0, loc)
	to := time.Date(local.Year(), local.Month(), local.Day(), end.Hour(), end.Minute(), 0, 0, loc)
	if !to.After(from) {
		if local.Before(to) {
			from = from.AddDate(0, 0, -1)
		} else {
			to = to.AddDate(0, 0, 1)
		}
	}
	if len(q.Days) > 0 && !slices.Contains(q.Days, int(from.Weekday())) {
		return now
	}
	if !local.Before(from) && local.Before(to) {
		return to.UTC()
	}
	return now
}

// A malformed source timestamp is terminally expired, not a poison journal
// entry that prevents later unrelated events from being processed.
func (e *Event) UnmarshalJSON(raw []byte) error {
	type plain Event
	wire := struct {
		OccurredAt json.RawMessage `json:"occurred_at"`
		*plain
	}{plain: (*plain)(e)}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	var text string
	_ = json.Unmarshal(wire.OccurredAt, &text)
	e.OccurredAt, _ = time.Parse(time.RFC3339Nano, text)
	return nil
}

// Complete enabled channels are checked before accepting the switch. Disabled
// channels may be saved as drafts. URL credentials belong to sealed secrets.
func validateChannelOptions(ch Channel) bool {
	for _, key := range []string{"server"} {
		if raw := ch.Params[key]; raw != "" {
			u, err := url.Parse(raw)
			if err != nil || !slicesURL(u) || u.RawQuery != "" {
				return false
			}
		}
	}
	if !ch.Enabled {
		return true
	}
	required := map[string][]string{"dingtalk": {"url"}, "dingtalk_app": {"app_key", "app_secret"}, "email": {}, "bark": {"key"}, "webhook": {"url"}, "wecom": {"url"}, "wecom_app": {"corp_id", "corp_secret"}, "serverchan": {"key"}, "telegram": {"token"}, "sms": {}, "feishu": {"url"}, "feishu_app": {"app_id", "app_secret"}, "pushplus": {"token"}, "gotify": {"token"}, "socket": {}}
	for _, key := range required[ch.Type] {
		secret := ch.Secrets[key]
		if secret.Env == "" && secret.Value == "" && len(secret.Ciphertext) == 0 && !secret.Set {
			return false
		}
	}
	fields := map[string][]string{"email": {"host", "from", "to"}, "sms": {"line_id", "to"}, "telegram": {"chat_id"}, "dingtalk_app": {"agent_id", "to"}, "wecom_app": {"agent_id", "to"}, "feishu_app": {"to", "receive_id_type"}, "gotify": {"server"}, "socket": {"protocol", "host", "port"}}
	for _, key := range fields[ch.Type] {
		if strings.TrimSpace(ch.Params[key]) == "" {
			return false
		}
	}
	switch ch.Type {
	case "email":
		if ch.Params["tls"] != "" && ch.Params["tls"] != "starttls" && ch.Params["tls"] != "implicit" {
			return false
		}
	case "bark":
		if ch.Params["encryption"] != "" && ch.Params["encryption"] != "none" && ch.Params["encryption"] != "aes-cbc" {
			return false
		}
	case "socket":
		if !slices.Contains([]string{"tcp", "udp", "mqtt"}, ch.Params["protocol"]) {
			return false
		}
		if ch.Params["protocol"] == "mqtt" && ch.Params["topic"] == "" {
			return false
		}
		if ch.Params["tls"] != "" && ch.Params["tls"] != "true" && ch.Params["tls"] != "false" {
			return false
		}
		if ch.Params["protocol"] == "udp" && ch.Params["tls"] == "true" {
			return false
		}
	}
	return true
}
