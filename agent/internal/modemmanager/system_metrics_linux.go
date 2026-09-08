//go:build linux

package modemmanager

import (
	"fmt"
	"syscall"
)

// statfsBytes reports the total and free byte capacity of the filesystem
// holding path.
func statfsBytes(path string) (uint64, uint64, error) {
	stats := &syscall.Statfs_t{}
	if err := syscall.Statfs(path, stats); err != nil {
		return 0, 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	total := stats.Blocks * uint64(stats.Bsize)
	free := stats.Bavail * uint64(stats.Bsize)
	return total, free, nil
}
