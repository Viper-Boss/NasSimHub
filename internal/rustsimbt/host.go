// SPDX-License-Identifier: AGPL-3.0-only
package rustsimbt

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func socketPath(dir string, channel byte) (string, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == "/" {
		return "", ErrInvalid
	}
	name := "control.sock"
	if channel == audioChannel {
		name = "audio.sock"
	} else if channel != controlChannel {
		return "", ErrInvalid
	}
	path := filepath.Join(dir, name)
	if len(path) > 102 {
		return "", ErrInvalid
	}
	return path, nil
}

func newViaHost(dir string, credential Credential) (*Client, error) {
	if credential.Agent == "" || len(credential.Agent) > 160 || credential.Key == [32]byte{} {
		return nil, ErrInvalid
	}
	if _, err := socketPath(dir, controlChannel); err != nil {
		return nil, err
	}
	return newClient(func(ctx context.Context, channel byte) (net.Conn, error) {
		path, err := socketPath(dir, channel)
		if err != nil {
			return nil, err
		}
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}, credential), nil
}

// ServeHost exposes only the two private Unix endpoints for one stick's RFCOMM
// channels. This lets a container use host Bluetooth without joining the host
// network namespace and without giving it access to the system D-Bus.
func ServeHost(ctx context.Context, dir, peer string) error {
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0700 {
		return errors.New("RustSimHub Bluetooth: host socket directory must have mode 0700")
	}
	dial, err := hostDial(peer)
	if err != nil {
		return err
	}
	control, err := socketPath(dir, controlChannel)
	if err != nil {
		return err
	}
	audio, err := socketPath(dir, audioChannel)
	if err != nil {
		return err
	}
	controlListener, err := net.Listen("unix", control)
	if err != nil {
		return err
	}
	defer controlListener.Close()
	if err := os.Chmod(control, 0600); err != nil {
		return err
	}
	audioListener, err := net.Listen("unix", audio)
	if err != nil {
		return err
	}
	defer audioListener.Close()
	if err := os.Chmod(audio, 0600); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() {
		controlListener.Close()
		audioListener.Close()
	})
	defer stop()
	finished := make(chan error, 2)
	go func() { finished <- serveSocket(ctx, controlListener, dial, controlChannel) }()
	go func() { finished <- serveSocket(ctx, audioListener, dial, audioChannel) }()
	err = <-finished
	cancel()
	<-finished
	return err
}

func serveSocket(ctx context.Context, listener net.Listener, dial dialFunc, channel byte) error {
	ctx, cancel := context.WithCancel(ctx)
	var tasks sync.WaitGroup
	defer tasks.Wait()
	defer cancel()
	bound := make(chan struct{}, 1)
	for {
		local, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case bound <- struct{}{}:
		default:
			local.Close()
			continue
		}
		tasks.Add(1)
		go func() {
			defer tasks.Done()
			defer func() { <-bound; local.Close() }()
			remote, err := dial(ctx, channel)
			if err != nil {
				return
			}
			defer remote.Close()
			stop := context.AfterFunc(ctx, func() { local.Close(); remote.Close() })
			defer stop()
			if channel == controlChannel {
				limit := time.Now().Add(45 * time.Second)
				_ = remote.SetDeadline(limit)
				_ = local.SetDeadline(limit)
			}
			done := make(chan struct{})
			go func() { bridgeLeg(remote, local, channel); done <- struct{}{} }()
			go func() { bridgeLeg(local, remote, channel); done <- struct{}{} }()
			<-done
			local.Close()
			remote.Close()
			<-done
		}()
	}
}

func bridgeLeg(dst, src net.Conn, channel byte) {
	if channel == controlChannel {
		_, _ = io.Copy(dst, src)
		return
	}
	block := make([]byte, 640)
	for {
		if src.SetReadDeadline(time.Now().Add(3*time.Second)) != nil {
			return
		}
		n, err := src.Read(block)
		if n == 0 || dst.SetWriteDeadline(time.Now().Add(60*time.Millisecond)) != nil {
			return
		}
		written, writeErr := dst.Write(block[:n])
		if written != n || writeErr != nil || err != nil {
			return
		}
	}
}
