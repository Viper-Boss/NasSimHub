package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/auth"
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
	// The header names the account whose contacts follow -- see
	// contactBackupVisible for why, and for why it has to be the very first
	// thing in the file.
	_, err = fmt.Fprintf(file, `%s"created_at":%q,"contacts":[`,
		contactBackupHeader(request.Context()), timestamp.Format(time.RFC3339Nano))
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
		"storage": "backups/contacts", "directory": api.contactBackupDirectory,
		"format": "homesim-contacts-v1",
	})
}

// The restore half of the backup feature.
//
// There was only a backup: the operator could write contacts to the NAS and
// had no way to read them back, which makes the button a one-way door. These
// two handlers are the missing half, and they are deliberately READ-ONLY.
//
// The restore itself is not done here. The client already has the import path
// the vCard and Google flows use -- it matches each incoming contact against
// the current ones by phone number, merges numbers into a match, refuses an
// ambiguous multi-match, and reports created/updated/skipped/failed. Reusing
// that means a restore behaves exactly like an import, which is what it is,
// and it means this file adds no second, untested bulk-write path into the
// contact store.
//
// The rule the backup handler above states -- never accept a client-supplied
// filesystem path -- holds here too, and these read the directory the server
// configuration owns. A requested name is matched against the exact shape the
// writer produces, and the resolved path is checked to be inside the directory
// afterwards as well, so neither check stands alone.

// contacts-20060102T150405Z-<suffix>.json, which is what backupContacts writes.
// The suffix is whatever os.CreateTemp chose, so it is bounded by character
// class and length rather than by value.
var contactBackupName = regexp.MustCompile(`^contacts-\d{8}T\d{6}Z-[A-Za-z0-9]{1,32}\.json$`)

// backupContactsCollection serves GET (list the backups) and POST (write one).
func (api *API) backupContactsCollection(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		api.listContactBackups(response, request)
	case http.MethodPost:
		api.backupContacts(response, request)
	default:
		response.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "Only GET and POST are supported", "")
	}
}

// Who may read a backup back.
//
// Contacts are per account: the store scopes every contact query to its
// owner, administrators included. The backup directory is shared, though, and
// the list and read handlers below used to serve every file in it to any
// signed-in account -- so one member pressing "back up" handed their whole
// address book to every other member, who did not even have to guess a name
// because the listing printed it.
//
// The writer therefore stamps the owning account into the file, and a file is
// visible only to that account. The stamp is the leading bytes of the file,
// written by the server before any contact data, and it is compared as bytes
// rather than parsed: nothing a contact contains can come before it, so a
// contact named like a header cannot claim a file for somebody else.
//
// A file with no owner stamp was written before this existed. Its owner is
// unknowable, so it is shown to the administrator only -- who owns the
// directory it sits in -- rather than to everyone (the leak) or to no one
// (the operator's existing backups vanishing on upgrade).
//
// With no principal there are no accounts to separate (authentication
// disabled, the same case requireAdmin lets through).
const contactBackupFormatPrefix = `{"format":"homesim-contacts-v1",`

func contactBackupOwnerPrefix(userID string) string {
	// json.Marshal, not %q: the two escape differently, and this must be
	// byte-identical between the writer and every later comparison.
	encoded, _ := json.Marshal(userID)
	return contactBackupFormatPrefix + `"owner_user_id":` + string(encoded) + `,`
}

func contactBackupHeader(ctx context.Context) string {
	if principal, ok := auth.PrincipalFromContext(ctx); ok {
		return contactBackupOwnerPrefix(principal.UserID)
	}
	return contactBackupFormatPrefix
}

func contactBackupVisible(ctx context.Context, path string) bool {
	principal, ok := auth.PrincipalFromContext(ctx)
	if !ok {
		return true
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	own := contactBackupOwnerPrefix(principal.UserID)
	unowned := contactBackupFormatPrefix + `"owner_user_id":`
	head := make([]byte, max(len(own), len(unowned)))
	read, _ := io.ReadFull(file, head)
	head = head[:read]
	if bytes.HasPrefix(head, []byte(own)) {
		return true
	}
	if bytes.HasPrefix(head, []byte(unowned)) {
		// Somebody else's.
		return false
	}
	return principal.IsAdmin()
}

type contactBackupEntry struct {
	Filename  string    `json:"filename"`
	CreatedAt time.Time `json:"created_at"`
	SizeBytes int64     `json:"size_bytes"`
}

func (api *API) listContactBackups(response http.ResponseWriter, request *http.Request) {
	if api.contactBackupDirectory == "" {
		writeError(response, http.StatusServiceUnavailable, "backup_unavailable", "NAS contact backup is not configured", "")
		return
	}
	entries, err := os.ReadDir(api.contactBackupDirectory)
	if err != nil {
		if os.IsNotExist(err) {
			// Nothing has been backed up yet. That is an empty list, not an
			// error: the operator has simply not pressed the button.
			response.Header().Set("Cache-Control", "no-store")
			writeJSON(response, http.StatusOK, map[string]any{"backups": []contactBackupEntry{}, "directory": api.contactBackupDirectory})
			return
		}
		api.writeInternalError(response, request, "list contact backups", err)
		return
	}
	backups := make([]contactBackupEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !contactBackupName.MatchString(entry.Name()) {
			continue
		}
		if !contactBackupVisible(request.Context(), filepath.Join(api.contactBackupDirectory, entry.Name())) {
			continue
		}
		info, statErr := entry.Info()
		if statErr != nil {
			continue
		}
		// The timestamp comes out of the NAME, not the modification time: a
		// copy or a restore of the backup directory rewrites mtimes, and the
		// name is what the writer stamped when the contacts were read.
		stamp, parseErr := time.Parse("20060102T150405Z", entry.Name()[len("contacts-"):len("contacts-")+16])
		if parseErr != nil {
			continue
		}
		backups = append(backups, contactBackupEntry{Filename: entry.Name(), CreatedAt: stamp.UTC(), SizeBytes: info.Size()})
	}
	// Newest first: the one an operator wants is almost always the last one
	// written, and it should not be at the bottom of a long list.
	sort.Slice(backups, func(i, j int) bool { return backups[i].Filename > backups[j].Filename })
	if len(backups) > 50 {
		backups = backups[:50]
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, map[string]any{"backups": backups, "directory": api.contactBackupDirectory})
}

// contactBackupFileName matches /api/v1/contacts/backup/<filename>.
func contactBackupFileName(path string) (string, bool) {
	const prefix = "/api/v1/contacts/backup/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	name := strings.TrimPrefix(path, prefix)
	if !contactBackupName.MatchString(name) {
		return "", false
	}
	return name, true
}

// readContactBackup returns one backup's JSON so the client can import it.
func (api *API) readContactBackup(response http.ResponseWriter, request *http.Request, name string) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "Only GET is supported", "")
		return
	}
	if api.contactBackupDirectory == "" {
		writeError(response, http.StatusServiceUnavailable, "backup_unavailable", "NAS contact backup is not configured", "")
		return
	}
	// The name already matched the writer's exact shape, which admits no
	// separator and no dot segment. This is the second, independent check: the
	// resolved path has to be a direct child of the directory. Either one alone
	// would do; both is what makes a future change to one of them survivable.
	root, err := filepath.Abs(api.contactBackupDirectory)
	if err != nil {
		api.writeInternalError(response, request, "resolve contact backup directory", err)
		return
	}
	full := filepath.Join(root, name)
	if filepath.Dir(full) != root {
		writeError(response, http.StatusNotFound, "not_found", "Backup not found", "")
		return
	}
	// Another account's backup answers exactly as a missing one does, so the
	// reply does not confirm that the name exists.
	if !contactBackupVisible(request.Context(), full) {
		writeError(response, http.StatusNotFound, "not_found", "Backup not found", "")
		return
	}
	file, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(response, http.StatusNotFound, "not_found", "Backup not found", "")
			return
		}
		api.writeInternalError(response, request, "open contact backup", err)
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		writeError(response, http.StatusNotFound, "not_found", "Backup not found", "")
		return
	}
	// The same ceiling the writer enforces. A file larger than that did not
	// come from the writer.
	if info.Size() > 32<<20 {
		writeError(response, http.StatusRequestEntityTooLarge, "backup_too_large", "Backup exceeds the 32 MiB limit", "")
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.Copy(response, file); err != nil {
		// The status and the body are already on the wire; nothing useful can
		// be said to the client now, so this is only worth recording.
		if !requestWasCanceled(request, err) {
			api.logger.Error("stream contact backup", "method", request.Method, "path", request.URL.Path, "error", err)
		}
	}
}
