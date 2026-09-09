//go:build linux

package vowifi

import (
	"errors"
	"syscall"
)

// bindToDeviceControl returns a socket control that pins the connection to
// interfaceName, so the cellular path probe cannot leak onto the default
// route. Requires CAP_NET_RAW; the agent already needs equivalent privileges
// for its DHCP work.
func bindToDeviceControl(interfaceName string) (func(string, string, syscall.RawConn) error, error) {
	return func(network, address string, connection syscall.RawConn) error {
		var controlErr error
		if err := connection.Control(func(fd uintptr) {
			controlErr = syscall.SetsockoptString(
				int(fd),
				syscall.SOL_SOCKET,
				syscall.SO_BINDTODEVICE,
				interfaceName+"\x00",
			)
		}); err != nil {
			return err
		}
		if controlErr != nil {
			if errors.Is(controlErr, syscall.EPERM) {
				return errBindUnavailable
			}
			return controlErr
		}
		return nil
	}, nil
}
