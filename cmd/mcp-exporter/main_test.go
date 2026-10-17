//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Console exporters write to stdout; in stdio mode that is the protocol
// channel, so the binary must keep it free of anything but server frames.
func TestStdioStdoutCarriesOnlyProtocol(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "mcp-exporter")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	const resp = `{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`
	server := `read -r line; echo '` + resp + `'; echo "server log line" >&2`

	cmd := exec.Command(bin, "stdio", "--", "sh", "-c", server)
	cmd.Env = append(os.Environ(),
		"OTEL_TRACES_EXPORTER=console", "OTEL_METRICS_EXPORTER=console", "OTEL_LOGS_EXPORTER=console")
	cmd.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x"}}` + "\n")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v\nstderr:\n%s", err, stderr.String())
	}

	if got := strings.TrimSpace(string(stdout)); got != resp {
		t.Errorf("stdout must be exactly the server's frame\ngot:\n%s", got)
	}
	for _, want := range []string{`"Name":"tools/call x"`, "mcp.server.operation.duration", "server log line"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q (telemetry should land there)", want)
		}
	}
}
