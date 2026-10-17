package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fakeResponse = `{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`

// With FAKE_MCP_SERVER=1 the test binary acts as a one-request stdio MCP
// server, so the tests need no shell or interpreter on any platform.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_MCP_SERVER") == "1" {
		bufio.NewReader(os.Stdin).ReadString('\n')
		fmt.Println(fakeResponse)
		fmt.Fprintln(os.Stderr, "server log line")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "mcp-exporter")
	if os.PathSeparator == '\\' {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// runStdio runs the exporter in stdio mode in front of the fake server.
func runStdio(t *testing.T, bin string, env ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.Command(bin, "stdio", "--", os.Args[0])
	cmd.Env = append(append(os.Environ(), "FAKE_MCP_SERVER=1"), env...)
	cmd.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x"}}` + "\n")
	var errb strings.Builder
	cmd.Stderr = &errb
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), errb.String(), err
}

func TestStdioRelaysFrames(t *testing.T) {
	stdout, stderr, err := runStdio(t, buildBinary(t),
		"OTEL_TRACES_EXPORTER=none", "OTEL_METRICS_EXPORTER=none", "OTEL_LOGS_EXPORTER=none")
	if err != nil {
		t.Fatalf("run: %v\nstderr:\n%s", err, stderr)
	}
	if stdout != fakeResponse {
		t.Errorf("stdout must be exactly the server's frame, got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "server log line") {
		t.Errorf("server stderr should be passed through, got:\n%s", stderr)
	}
}
