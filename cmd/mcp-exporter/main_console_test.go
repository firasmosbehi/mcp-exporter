package main

import (
	"strings"
	"testing"
)

// In stdio mode stdout is the protocol channel: with every console exporter
// on, it must carry nothing but server frames, on every platform (#51).
func TestStdioStdoutCarriesOnlyProtocol(t *testing.T) {
	stdout, stderr, err := runStdio(t, buildBinary(t),
		"OTEL_TRACES_EXPORTER=console", "OTEL_METRICS_EXPORTER=console", "OTEL_LOGS_EXPORTER=console")
	if err != nil {
		t.Fatalf("run: %v\nstderr:\n%s", err, stderr)
	}
	if stdout != fakeResponse {
		t.Errorf("stdout must be exactly the server's frame\ngot:\n%s", stdout)
	}
	for _, want := range []string{`"Name":"tools/call x"`, "mcp.server.operation.duration"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q (telemetry should land there)", want)
		}
	}
}
