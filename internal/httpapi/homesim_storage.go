package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// writeHomeSIMJSON persists a payload atomically inside the HomeSIM data
// directory (temp file + same-directory rename, 0600 like the other
// HomeSIM server-side files).
func writeHomeSIMJSON(path string, payload any) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".homesim-*")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(payload); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
