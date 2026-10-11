package localadmin

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type rememberedSessions struct {
	DeviceID   string             `json:"device_id"`
	Credential string             `json:"credential"`
	Sessions   map[string]session `json:"sessions"`
}

func sessionKey(token string) string {
	h := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// Cookie bearer tokens never reach disk. Sessions keep their original expiry.
func (a *Admin) persistSessions() error {
	data, err := json.Marshal(rememberedSessions{a.options.DeviceID, sessionKey(a.cred.Salt + a.cred.Hash), a.sessions})
	if err != nil {
		return err
	}
	return writePrivate(a.options.Dir, "admin-sessions.json", data)
}
func (a *Admin) restoreSessions() error {
	data, err := os.ReadFile(filepath.Join(a.options.Dir, "admin-sessions.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var stored rememberedSessions
	if json.Unmarshal(data, &stored) != nil {
		return errors.New("invalid local administrator remembered sessions")
	}
	if stored.DeviceID != a.options.DeviceID || stored.Credential != sessionKey(a.cred.Salt+a.cred.Hash) {
		return nil
	}
	if len(stored.Sessions) > maxSessions {
		return errors.New("local administrator remembered session limit exceeded")
	}
	for id, s := range stored.Sessions {
		if len(id) != 43 || len(s.CSRF) != 43 {
			return errors.New("invalid local administrator remembered session")
		}
		if !a.expired(s, a.options.Now()) {
			a.sessions[id] = s
		}
	}
	return nil
}
