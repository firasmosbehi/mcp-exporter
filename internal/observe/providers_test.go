package observe

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Gateway routes (#54) each get their own providers; with the Prometheus
// exporter they must share one /metrics and stay apart by service_name.
func TestProvidersPerRouteSharePrometheus(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	l.Close()
	t.Setenv("OTEL_METRICS_EXPORTER", "prometheus")
	t.Setenv("OTEL_EXPORTER_PROMETHEUS_HOST", "127.0.0.1")
	t.Setenv("OTEL_EXPORTER_PROMETHEUS_PORT", port)
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_LOGS_EXPORTER", "none")

	for _, name := range []string{"github", "jira"} {
		p, err := NewProviders(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Shutdown(context.Background())
		tr := NewTracker(Options{Transport: "tcp", Providers: p})
		tr.Frame(context.Background(), ClientToServer, "s", "", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"t"}}`))
		tr.Frame(context.Background(), ServerToClient, "s", "", []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}

	var body string
	for i := 0; i < 50; i++ { // the listener starts in the background
		if resp, err := http.Get("http://127.0.0.1:" + port + "/metrics"); err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			body = string(b)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, want := range []string{
		`mcp_server_operation_duration_seconds_count{`,
		`service_name="github"`,
		`service_name="jira"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %s:\n%s", want, body)
		}
	}
}
