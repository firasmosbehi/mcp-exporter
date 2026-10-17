//go:build unix

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// reserveStdout hands back the real stdout for protocol frames and points fd 1
// at stderr, so nothing else in the process (console exporters capture
// os.Stdout at init) can write into the JSON-RPC stream.
func reserveStdout() (*os.File, error) {
	fd, err := unix.Dup(1)
	if err != nil {
		return nil, err
	}
	if err := unix.Dup2(2, 1); err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "protocol-stdout"), nil
}
