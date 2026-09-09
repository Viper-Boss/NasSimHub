//go:build !linux

package vowifi

import "syscall"

// bindToDeviceControl is unavailable off Linux; the cellular path probe
// degrades to an unbound query instead.
func bindToDeviceControl(interfaceName string) (func(string, string, syscall.RawConn) error, error) {
	return nil, errBindUnavailable
}
