//go:build !unix

package main

import "os"

// reserveStdout has no descriptor swap here. Console exporters write to stderr
// explicitly (internal/observe), so only third-party stdout writes could reach
// the protocol stream.
func reserveStdout() (*os.File, error) { return os.Stdout, nil }
