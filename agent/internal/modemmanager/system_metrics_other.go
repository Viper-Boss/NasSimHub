//go:build !linux

package modemmanager

import "fmt"

// statfsBytes is unavailable off Linux; system metrics simply omit flash
// usage there.
func statfsBytes(path string) (uint64, uint64, error) {
	return 0, 0, fmt.Errorf("statfs is not supported on this platform")
}
