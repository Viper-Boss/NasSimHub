// SPDX-License-Identifier: AGPL-3.0-only
package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"

	"github.com/human-agent65535/modemdeck/internal/auth"
	"github.com/human-agent65535/modemdeck/internal/forwarding"
)

func readLegacyForwarding(path string, destination any) (bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, forwarding.ErrUnavailable
	}
	if len(raw) > 1<<20 || json.Unmarshal(raw, destination) != nil {
		return false, forwarding.ErrInvalid
	}
	return true, nil
}
func (api *API) migrateForwarding(w http.ResponseWriter, r *http.Request, current forwarding.Config, principal auth.Principal) {
	var input struct {
		Revision int64 `json:"revision"`
	}
	if !decodeJSONBody(w, r, &input) {
		return
	}
	if input.Revision != current.Revision || len(current.Channels) > 0 || len(current.Rules) > 0 {
		api.forwardingFailure(w, forwarding.ErrConflict)
		return
	}
	next := forwarding.DefaultConfig()
	next.Revision = current.Revision
	next.OwnerID = principal.UserID
	next.Enabled = false
	skipped := []string{}
	add := func(channel forwarding.Channel, events, lines []string) {
		next.Channels = append(next.Channels, channel)
		next.Rules = append(next.Rules, forwarding.Rule{ID: "rule_" + channel.ID, Name: channel.Name, Enabled: channel.Enabled, Events: events, LineIDs: lines, ChannelIDs: []string{channel.ID}, Logic: "all", TemplateID: "default", Condition: forwarding.Condition{Op: "always"}, Quiet: forwarding.QuietHours{Start: "22:00", End: "08:00", Timezone: "Asia/Shanghai", Days: []int{}}})
	}
	path, ok := api.homeSIMForwarderPath()
	if !ok {
		api.forwardingFailure(w, forwarding.ErrUnavailable)
		return
	}
	var old homeSIMForwarderSettings
	found, err := readLegacyForwarding(path, &old)
	if err != nil {
		api.forwardingFailure(w, err)
		return
	}
	if found {
		if !validHomeSIMForwarderSettings(old) {
			api.forwardingFailure(w, forwarding.ErrInvalid)
			return
		}
		for i, channel := range old.Forwarders {
			if channel.Type == "nkn" {
				skipped = append(skipped, "nkn_sms")
				continue
			}
			converted := forwarding.Channel{ID: fmt.Sprintf("legacy_%d", i), Name: channel.Name, Type: channel.Type, Enabled: channel.Enabled, Params: map[string]string{}, Secrets: map[string]forwarding.Secret{}}
			switch channel.Type {
			case "webhook", "wecom", "feishu", "dingtalk":
				converted.Secrets["url"] = forwarding.Secret{Value: channel.Config.WebhookURL}
				if channel.Config.Secret != "" {
					converted.Secrets["signing_key"] = forwarding.Secret{Value: channel.Config.Secret}
				}
			case "serverchan":
				converted.Secrets["key"] = forwarding.Secret{Env: channel.Config.CredentialRef}
			case "pushplus":
				converted.Secrets["token"] = forwarding.Secret{Env: channel.Config.CredentialRef}
			}
			add(converted, []string{"incoming_sms"}, append([]string(nil), principal.AllowedLineIDs...))
		}
	}
	barkPath, ok := api.homeSIMBarkPath()
	if !ok {
		api.forwardingFailure(w, forwarding.ErrUnavailable)
		return
	}
	var bark homeSIMBarkSettings
	found, err = readLegacyForwarding(barkPath, &bark)
	if err != nil {
		api.forwardingFailure(w, err)
		return
	}
	if found {
		if !validHomeSIMBarkSettings(bark) {
			api.forwardingFailure(w, forwarding.ErrInvalid)
			return
		}
		add(forwarding.Channel{ID: "legacy_bark", Name: "Bark", Type: "bark", Enabled: false, Params: map[string]string{"server": bark.ServerURL, "group": bark.Group, "sound": bark.Sound}, Secrets: map[string]forwarding.Secret{"key": {Env: bark.CredentialRef}}}, []string{"incoming_sms"}, append([]string(nil), principal.AllowedLineIDs...))
	}
	// Existing bot commands stay where they are. Only this administrator's
	// explicitly scoped notification settings may be copied; no authority widens.
	if api.telegram != nil {
		units, listErr := api.telegram.List(r.Context())
		if listErr != nil {
			api.forwardingFailure(w, forwarding.ErrUnavailable)
			return
		}
		for i, unit := range units {
			if !unit.IncomingSMS && !unit.MissedCalls {
				continue
			}
			if unit.AssignedUserID != principal.UserID || !unit.TokenConfigured {
				skipped = append(skipped, "telegram_requires_owner_review")
				continue
			}
			lines := []string{}
			for _, id := range principal.AllowedLineIDs {
				if unit.AllAssignedLines || slices.Contains(unit.LineScopes, id) {
					lines = append(lines, id)
				}
			}
			if len(lines) == 0 {
				skipped = append(skipped, "telegram_no_permitted_line")
				continue
			}
			// Tokens stay encrypted in the original bot store. Explicitly importing a
			// Telegram notification requires a new environment reference, never a token
			// extraction API. The disabled channel must be completed before enabling.
			events := []string{}
			if unit.IncomingSMS {
				events = append(events, "incoming_sms")
			}
			if unit.MissedCalls {
				events = append(events, "missed_call")
			}
			add(forwarding.Channel{ID: fmt.Sprintf("legacy_telegram_%d", i), Name: unit.DisplayName, Type: "telegram", Enabled: false, Params: map[string]string{"chat_id": unit.ChatID}, Secrets: map[string]forwarding.Secret{"token": {Env: "FORWARDING_TELEGRAM_TOKEN_" + strconv.Itoa(i)}}}, events, lines)
			skipped = append(skipped, "telegram_token_reference_required")
		}
	}
	sealed, err := forwarding.SealConfig(next, current, api.settingsSecrets)
	if err == nil {
		sealed, err = api.forwardingStore.Save(r.Context(), sealed)
	}
	if err != nil {
		api.forwardingFailure(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"config": sealed.Public(), "skipped": skipped, "legacy_files_retained": true, "enabled": false})
}
