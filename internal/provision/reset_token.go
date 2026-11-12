package provision

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ResetTokenName is read locally by the operator, never returned over HTTP.
const ResetTokenName = ".reset-confirmation-token"

// EnsureResetToken creates a private 256-bit confirmation code once. It does
// not request a reset. Existing codes survive API restarts until the reset.
func EnsureResetToken(directory string) error {
	if strings.TrimSpace(directory) == "" {
		return fmt.Errorf("provision: no data directory")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	code := make([]byte, 32)
	if _, err := rand.Read(code); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(directory, ResetTokenName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	_, writeErr := io.WriteString(file, hex.EncodeToString(code)+"\n")
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// CheckResetToken fails closed on absent, corrupt or unreadable local codes.
func CheckResetToken(directory, supplied string) bool {
	file, err := os.Open(filepath.Join(directory, ResetTokenName))
	if err != nil {
		return false
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 128))
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(expected) != 32 {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimSpace(supplied))
	return err == nil && subtle.ConstantTimeCompare(expected, provided) == 1
}
