//go:build !unix

package main

import (
	"strings"
	"testing"
)

// Issue #10: without an fd-level swap, console exporters must be refused in
// stdio mode rather than allowed to corrupt the protocol stream.
func TestStdioRefusesConsoleExporters(t *testing.T) {
	stdout, stderr, err := runStdio(t, buildBinary(t), "OTEL_TRACES_EXPORTER=console")
	if err == nil {
		t.Fatalf("want a non-zero exit, got stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "OTEL_TRACES_EXPORTER=console") || stdout != "" {
		t.Errorf("want a clear error and no stdout output; stderr:\n%s\nstdout:\n%s", stderr, stdout)
	}
}
