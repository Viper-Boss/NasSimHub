// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/human-agent65535/modemdeck/internal/rustsimbt"
)

func run() error {
	if len(os.Args) == 4 && os.Args[1] == "relay" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return rustsimbt.ServeHost(ctx, os.Args[3], os.Args[2])
	}
	if (len(os.Args) != 5 && len(os.Args) != 6) || os.Args[1] != "pair" {
		return errors.New("usage: rustsimhub-bluetooth pair STICK_MAC LINE_ID ABSOLUTE_PRIVATE_FILE [CONTAINER_SOCKET_DIRECTORY] < pairing-code; or relay STICK_MAC PRIVATE_SOCKET_DIRECTORY")
	}
	path := os.Args[4]
	line := os.Args[3]
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.TrimSpace(line) == "" || len(line) > 128 || strings.ContainsAny(line, "\r\n\x00") {
		return rustsimbt.ErrInvalid
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode().Perm() != 0700 {
		return errors.New("configuration directory must be private with mode 0700")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("cannot create new private configuration")
	}
	keep := false
	defer func() {
		file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	// Never put the code or secret into a command argument or output.
	lineInput, _, err := bufio.NewReaderSize(os.Stdin, 32).ReadLine()
	if err != nil || len(lineInput) != 6 {
		return rustsimbt.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	credential, err := rustsimbt.Pair(ctx, os.Args[2], string(lineInput))
	clear(lineInput)
	if err != nil {
		return err
	}
	config := rustsimbt.Config{Peer: os.Args[2], LineID: line, Credential: credential}
	if len(os.Args) == 6 {
		config.SocketDirectory = os.Args[5]
	}
	if err := json.NewEncoder(file).Encode(config); err != nil {
		return errors.Join(rustsimbt.ErrUnknown, err)
	}
	if err := file.Sync(); err != nil {
		return errors.Join(rustsimbt.ErrUnknown, err)
	}
	if err := file.Close(); err != nil {
		return errors.Join(rustsimbt.ErrUnknown, err)
	}
	keep = true
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
