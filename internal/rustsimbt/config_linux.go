// SPDX-License-Identifier: AGPL-3.0-only
package rustsimbt

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func LoadConfig(path string) (Config, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return Config{}, ErrInvalid
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return Config{}, errors.New("RustSimHub Bluetooth: cannot open private configuration")
	}
	file := os.NewFile(uintptr(fd), "rustsimhub-bt-config")
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG ||
		stat.Mode&0777 != 0600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || stat.Size > 4096 {
		return Config{}, errors.New("RustSimHub Bluetooth: configuration must be a private regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(raw) > 4096 {
		return Config{}, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var config Config
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || !validLine(config.LineID) {
		return Config{}, ErrInvalid
	}
	return config, nil
}
