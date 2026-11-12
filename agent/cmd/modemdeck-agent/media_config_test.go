package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/human-agent65535/modemdeck/agent/internal/media"
)

func TestLoadMediaBindings(t *testing.T) {
	path := writeMediaConfig(t, `{
		"bindings": [
			{
				"audio_port": "usb:2-1.3",
				"backend": "char-pcm",
				"endpoint": "/dev/modemdeck-pcm0"
			}
		]
	}`)
	bindings, err := loadMediaBindings(path)
	if err != nil {
		t.Fatalf("loadMediaBindings: %v", err)
	}
	if len(bindings) != 1 || bindings[0] != (media.Binding{
		AudioPort: "usb:2-1.3",
		Backend:   media.BackendCharPCM,
		Endpoint:  "/dev/modemdeck-pcm0",
	}) {
		t.Fatalf("unexpected bindings: %+v", bindings)
	}
}

func TestLoadMediaBindingsRejectsInvalidFiles(t *testing.T) {
	tests := map[string]string{
		"unknown field":   `{"bindings":[],"fallback":true}`,
		"multiple values": `{"bindings":[]} {"bindings":[]}`,
		"truncated":       `{"bindings":[`,
	}
	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := loadMediaBindings(writeMediaConfig(t, contents)); err == nil {
				t.Fatal("loadMediaBindings unexpectedly succeeded")
			}
		})
	}
}

func TestLoadMediaBindingsRejectsOversizedFile(t *testing.T) {
	path := writeMediaConfig(t, strings.Repeat(" ", maxMediaBindingsFileBytes+1))
	if _, err := loadMediaBindings(path); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadMediaBindingsRequiresAbsolutePath(t *testing.T) {
	if _, err := loadMediaBindings("media.json"); err == nil {
		t.Fatal("relative path unexpectedly accepted")
	}
	bindings, err := loadMediaBindings("")
	if err != nil || len(bindings) != 0 {
		t.Fatalf("empty path = %+v, %v", bindings, err)
	}
}

func TestConfigureMediaBackendsDoesNotRequireALSAForCharacterPCM(t *testing.T) {
	backends, err := configureMediaBackendsWithOpener([]media.Binding{{
		AudioPort: "usb:2-1.3",
		Backend:   media.BackendCharPCM,
		Endpoint:  "/dev/modemdeck-pcm0",
	}}, func() (media.ALSAOpener, error) { return nil, errors.New("alsa-utils unavailable") })
	if err != nil {
		t.Fatalf("configureMediaBackends: %v", err)
	}
	if backends[media.BackendCharPCM] == nil {
		t.Fatal("char-pcm backend is missing")
	}
	if backends[media.BackendALSAPCM] != nil {
		t.Fatal("ALSA backend was configured despite unavailable alsa-utils")
	}
}

func writeMediaConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "media-bindings.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestConfigureMediaBackendsDiscoversALSAWithoutExplicitBinding(t *testing.T) {
	backends, err := configureMediaBackendsWithOpener(nil, func() (media.ALSAOpener, error) {
		return &media.ALSACommandOpener{}, nil
	})
	if err != nil || backends[media.BackendALSAPCM] == nil {
		t.Fatalf("derived UAC bindings need an ALSA backend: %v", err)
	}
	_, err = configureMediaBackendsWithOpener([]media.Binding{{Backend: media.BackendALSAPCM}}, func() (media.ALSAOpener, error) {
		return nil, errors.New("alsa-utils unavailable")
	})
	if err == nil {
		t.Fatal("an explicit unavailable ALSA backend was accepted")
	}
}

func TestLoadMediaBindingsMissingFile(t *testing.T) {
	_, err := loadMediaBindings(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected error: %v", err)
	}
}
