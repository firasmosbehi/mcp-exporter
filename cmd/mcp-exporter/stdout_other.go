//go:build !unix

package main

import "os"

// ponytail: no fd swap off unix; don't use OTEL_*_EXPORTER=console in stdio mode there.
func reserveStdout() (*os.File, error) { return os.Stdout, nil }
