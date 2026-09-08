package httpapi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNKNWalletImportCannotOverwrite(t *testing.T) {
	m := newTestNKNNodeManager(t)
	if _, err := m.importWallet(testKeystore, "original-password"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(m.dir, nknNodeWalletFile))
	if _, err := m.importWallet(testKeystore, "replacement-password"); err == nil {
		t.Fatal("existing wallet overwritten")
	}
	after, _ := os.ReadFile(filepath.Join(m.dir, nknNodeWalletFile))
	password, _ := os.ReadFile(filepath.Join(m.dir, nknNodePasswordFile))
	if string(before) != string(after) || string(password) != "original-password\n" {
		t.Fatal("original wallet changed")
	}
}

func TestNKNWalletRejectsMultilinePassword(t *testing.T) {
	m := newTestNKNNodeManager(t)
	if _, err := m.createWallet("password\nextra"); err == nil {
		t.Fatal("multiline password accepted")
	}
	if _, err := m.importWallet(testKeystore, "password\nextra"); err == nil {
		t.Fatal("multiline import password accepted")
	}
}
