// SPDX-License-Identifier: AGPL-3.0-only
//go:build !linux

package rustsimbt

import (
	"context"
	"errors"
)

var errPlatform = errors.New("RustSimHub Bluetooth requires Linux")

func New(string, Credential) (*Client, error)                  { return nil, errPlatform }
func Pair(context.Context, string, string) (Credential, error) { return Credential{}, errPlatform }
func LoadConfig(string) (Config, error)                        { return Config{}, errPlatform }

func hostDial(string) (dialFunc, error) { return nil, errPlatform }
