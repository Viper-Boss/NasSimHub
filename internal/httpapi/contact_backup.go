package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/store"
)

// Backup contacts visible to this authenticated principal, not browser search
// results. Do not accept a client-supplied filesystem path or contact payload.
// A failed export leaves no partial JSON. Server configuration owns the root.
func (api *API) backupContacts(response http.ResponseWriter, request *http.Request) {
	if !api.contactBackupMu.TryLock() {
		writeError(response, 409, "backup_busy", "A contact backup is already running", "")
		return
	}
	defer api.contactBackupMu.Unlock()
	if api.contactBackupDirectory == "" {
		writeError(response, http.StatusServiceUnavailable, "backup_unavailable", "NAS contact backup is not configured", "")
		return
	}
	if err := os.MkdirAll(api.contactBackupDirectory, 0700); err != nil {
		api.writeInternalError(response, request, "prepare contact backup", err)
		return
	}
	file, err := os.CreateTemp(api.contactBackupDirectory, ".contacts-*.partial")
	if err != nil {
		api.writeInternalError(response, request, "create contact backup", err)
		return
	}
	temporary := file.Name()
	defer func() { _ = file.Close(); _ = os.Remove(temporary) }()
	timestamp := time.Now().UTC()
	count := 0
	_, err = fmt.Fprintf(file, `{"format":"homesim-contacts-v1","created_at":%q,"contacts":[`, timestamp.Format(time.RFC3339Nano))
	encoder := json.NewEncoder(file)
	var after *store.ContactCursor
	for err == nil {
		page, readErr := api.repository.Contacts(request.Context(), store.ContactQuery{Limit: 50, After: after})
		if readErr != nil {
			err = readErr
			break
		}
		for _, contact := range page {
			if count >= 10000 {
				err = fmt.Errorf("contact backup exceeds 10000-contact limit")
				break
			}
			if count > 0 {
				_, err = file.WriteString(",")
			}
			if err == nil {
				err = encoder.Encode(contact)
			}
			if err != nil {
				break
			}
			count++
		}
		if err != nil {
			break
		}
		info, statErr := file.Stat()
		if statErr != nil {
			err = statErr
			break
		}
		if info.Size() > 32<<20 {
			err = fmt.Errorf("contact backup exceeds 32 MiB limit")
			break
		}
		if len(page) < 50 {
			break
		}
		last := page[len(page)-1]
		if after != nil && last.ID == after.ID {
			err = fmt.Errorf("contact backup cursor did not advance")
			break
		}
		after = &store.ContactCursor{DisplayName: last.DisplayName, ID: last.ID}
	}
	if err == nil {
		_, err = file.WriteString("]}\n")
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	name := "contacts-" + timestamp.Format("20060102T150405Z") + "-" + strings.TrimSuffix(strings.TrimPrefix(filepath.Base(temporary), ".contacts-"), ".partial") + ".json"
	if err == nil {
		err = os.Rename(temporary, filepath.Join(api.contactBackupDirectory, name))
	}
	if err != nil {
		api.writeInternalError(response, request, "backup contacts", err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusCreated, map[string]any{
		"filename": name, "count": count, "created_at": timestamp,
		"storage": "backups/contacts", "format": "homesim-contacts-v1",
	})
}
