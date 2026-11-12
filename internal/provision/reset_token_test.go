package provision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalResetTokenIsStableAndFailsClosed(t *testing.T) {
	directory := t.TempDir()
	if CheckResetToken(directory, "") {
		t.Fatal("missing token accepted")
	}
	if err := EnsureResetToken(directory); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, ResetTokenName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.TrimSpace(string(raw))
	if !CheckResetToken(directory, code) || CheckResetToken(directory, "wrong") {
		t.Fatal("invalid token validation")
	}
	if err := EnsureResetToken(directory); err != nil {
		t.Fatal(err)
	}
	if !CheckResetToken(directory, code) {
		t.Fatal("a status poll rotated the operator's code")
	}
	if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if CheckResetToken(directory, code) || CheckResetToken(directory, "broken") {
		t.Fatal("corrupt token accepted")
	}
}
