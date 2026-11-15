// SPDX-License-Identifier: AGPL-3.0-only
package rustsimbt

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	solBluetooth   = 274
	btSecurity     = 4
	btSecurityHigh = 3
)

type rfcommAddr string

func (a rfcommAddr) Network() string { return "rfcomm" }
func (a rfcommAddr) String() string  { return string(a) }

type rfcommConn struct {
	*os.File
	peer rfcommAddr
}

func (c *rfcommConn) LocalAddr() net.Addr  { return rfcommAddr("local") }
func (c *rfcommConn) RemoteAddr() net.Addr { return c.peer }

func peerBytes(value string) ([6]byte, error) {
	var out [6]byte
	parts := strings.Split(value, ":")
	if len(parts) != len(out) {
		return out, ErrInvalid
	}
	for i, part := range parts {
		b, err := hex.DecodeString(part)
		if err != nil || len(b) != 1 {
			return out, ErrInvalid
		}
		out[len(out)-1-i] = b[0]
	}
	if out == [6]byte{} || out == [6]byte{255, 255, 255, 255, 255, 255} {
		return out, ErrInvalid
	}
	return out, nil
}

func dialRFCOMM(peer string) (dialFunc, error) {
	addr, err := peerBytes(peer)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, channel byte) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_STREAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, unix.BTPROTO_RFCOMM)
		if err != nil {
			return nil, err
		}
		handedOff := false
		defer func() {
			if !handedOff {
				_ = unix.Close(fd)
			}
		}()
		// Linux struct bt_security { uint8_t level, key_size; }.
		// Request an authenticated, encrypted link. BlueZ administers bonds.
		key := []byte{btSecurityHigh, 0}
		if err := unix.SetsockoptString(fd, solBluetooth, btSecurity, string(key)); err != nil {
			return nil, err
		}
		err = unix.Connect(fd, &unix.SockaddrRFCOMM{Addr: addr, Channel: channel})
		if errors.Is(err, unix.EINPROGRESS) || errors.Is(err, unix.EAGAIN) {
			err = waitConnected(ctx, fd)
		}
		if err != nil {
			return nil, err
		}
		file := os.NewFile(uintptr(fd), "rustsimhub-rfcomm")
		if file == nil {
			return nil, ErrInvalid
		}
		handedOff = true
		return &rfcommConn{File: file, peer: rfcommAddr(peer)}, nil
	}, nil
}

func waitConnected(ctx context.Context, fd int) error {
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
	for ctx.Err() == nil {
		n, err := unix.Poll(poll, 100)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		status, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ERROR)
		if err != nil {
			return err
		}
		if status != 0 {
			return unix.Errno(status)
		}
		return nil
	}
	return ctx.Err()
}

func New(peer string, credential Credential) (*Client, error) {
	if credential.Agent == "" || len(credential.Agent) > 160 || credential.Key == [32]byte{} {
		return nil, ErrInvalid
	}
	dial, err := dialRFCOMM(peer)
	if err != nil {
		return nil, err
	}
	return newClient(dial, credential), nil
}

func Pair(ctx context.Context, peer, code string) (Credential, error) {
	dial, err := dialRFCOMM(peer)
	if err != nil {
		return Credential{}, err
	}
	return enroll(ctx, dial, code)
}

func hostDial(peer string) (dialFunc, error) { return dialRFCOMM(peer) }
