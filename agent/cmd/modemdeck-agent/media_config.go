package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/human-agent65535/modemdeck/agent/internal/media"
)

const (
	maxMediaBindingsFileBytes = 64 << 10
	maxMediaBindings          = 64
)

type mediaBindingsConfig struct {
	Bindings []media.Binding `json:"bindings"`
}

func loadMediaBindings(path string) ([]media.Binding, error) {
	if path == "" {
		return []media.Binding{}, nil
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return nil, errors.New("media bindings file path must be absolute")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open media bindings file: %w", err)
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maxMediaBindingsFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read media bindings file: %w", err)
	}
	if len(contents) > maxMediaBindingsFileBytes {
		return nil, fmt.Errorf(
			"media bindings file exceeds %d bytes",
			maxMediaBindingsFileBytes,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var config mediaBindingsConfig
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode media bindings file: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("media bindings file must contain one JSON object")
	}
	if len(config.Bindings) > maxMediaBindings {
		return nil, fmt.Errorf("media bindings file contains more than %d bindings", maxMediaBindings)
	}
	return append([]media.Binding(nil), config.Bindings...), nil
}

func configureMediaBackends(
	bindings []media.Binding,
) (map[media.BackendKind]media.Backend, error) {
	return configureMediaBackendsWithOpener(bindings, func() (media.ALSAOpener, error) {
		return media.NewALSACommandOpener()
	})
}

func configureMediaBackendsWithOpener(bindings []media.Binding, open func() (media.ALSAOpener, error)) (map[media.BackendKind]media.Backend, error) {
	backends := map[media.BackendKind]media.Backend{
		media.BackendCharPCM: media.NewCharPCMBackend(nil),
	}
	requiresALSA := false
	for _, binding := range bindings {
		if binding.Backend == media.BackendALSAPCM {
			requiresALSA = true
			break
		}
	}
	opener, err := open()
	if err != nil {
		// A binding that NAMES this backend and cannot have it is a
		// misconfiguration worth refusing to start over: the operator asked for
		// something this host cannot do.
		if requiresALSA {
			return nil, err
		}
		// Otherwise the host simply has no alsa-utils. Carrying on without the
		// backend is correct -- char-pcm lines still work -- and the ports that
		// needed it report themselves unconfigured, which is true.
		slog.Warn(
			"alsa-utils is not installed, so module USB audio lines cannot carry call audio",
			"component", "media",
			"error", err,
		)
		return backends, nil
	}
	// Built whether or not a binding names it, because a Quectel UAC port's
	// binding is DERIVED rather than written in the file (see
	// media/quectel_uac_binding.go). Building it only on demand made the
	// derivation useless on exactly the installations it exists for: no
	// bindings file means no binding names alsa-pcm, so the backend was absent,
	// so the derived binding was unusable, so media stayed unavailable.
	backends[media.BackendALSAPCM] = media.NewALSAPCMBackend(opener)
	return backends, nil
}
