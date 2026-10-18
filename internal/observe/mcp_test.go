package observe

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func setup(t *testing.T) (*Tracker, *tracetest.InMemoryExporter, *sdkmetric.ManualReader) {
	spans := tracetest.NewInMemoryExporter()
	reader := sdkmetric.NewManualReader()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans)))
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return NewTracker(Options{Transport: "pipe", InjectContext: true, CapturePayloads: true}), spans, reader
}

func attrs(s tracetest.SpanStub) map[attribute.Key]string {
	m := map[attribute.Key]string{}
	for _, kv := range s.Attributes {
		m[kv.Key] = kv.Value.Emit()
	}
	return m
}

func send(tr *Tracker, dir Dir, msg string) string {
	return string(tr.Frame(context.Background(), dir, "s", "", []byte(msg)))
}

func TestToolCallSpanAndMetrics(t *testing.T) {
	tr, spans, reader := setup(t)

	send(tr, ClientToServer, `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"claude"}}}`)
	send(tr, ServerToClient, `{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":"2025-11-25","serverInfo":{"name":"weather"}}}`)

	out := send(tr, ClientToServer, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_weather","arguments":{"city":"Tunis","api_key":"sk-123"}}}`)
	if !strings.Contains(out, `"traceparent"`) {
		t.Errorf("traceparent not injected: %s", out)
	}
	send(tr, ServerToClient, `{"jsonrpc":"2.0","id":1,"result":{"isError":true,"content":[]}}`)

	got := spans.GetSpans()
	if len(got) != 2 {
		t.Fatalf("want 2 spans, got %d", len(got))
	}
	s := got[1]
	a := attrs(s)
	if s.Name != "tools/call get_weather" || s.SpanKind != trace.SpanKindServer {
		t.Errorf("name=%q kind=%v", s.Name, s.SpanKind)
	}
	for k, want := range map[attribute.Key]string{
		"mcp.method.name":       "tools/call",
		"gen_ai.tool.name":      "get_weather",
		"gen_ai.operation.name": "execute_tool",
		"jsonrpc.request.id":    "1",
		"mcp.protocol.version":  "2025-11-25",
		"mcp.server.name":       "weather",
		"mcp.client.name":       "claude",
		"network.transport":     "pipe",
		"error.type":            "tool_error",
	} {
		if a[k] != want {
			t.Errorf("%s = %q, want %q", k, a[k], want)
		}
	}
	if s.Status.Code != codes.Error {
		t.Error("tool_error should set span status Error")
	}
	if args := a["gen_ai.tool.call.arguments"]; strings.Contains(args, "sk-123") || !strings.Contains(args, "Tunis") {
		t.Errorf("arguments not redacted correctly: %s", args)
	}

	var rm metricdata.ResourceMetrics
	reader.Collect(context.Background(), &rm)
	var calls uint64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "mcp.server.operation.duration" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Histogram[float64]).DataPoints {
				if v, _ := dp.Attributes.Value("gen_ai.tool.name"); v.AsString() == "get_weather" {
					calls += dp.Count
					if e, _ := dp.Attributes.Value("error.type"); e.AsString() != "tool_error" {
						t.Errorf("metric error.type = %q", e.AsString())
					}
				}
			}
		}
	}
	if calls != 1 {
		t.Errorf("want 1 recorded tool call, got %d", calls)
	}
}

func TestErrorClassificationAndPropagation(t *testing.T) {
	tr, spans, _ := setup(t)

	// Caller mistake (-32602): not a server error, but the code is recorded.
	send(tr, ClientToServer, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x"}}`)
	send(tr, ServerToClient, `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"bad args"}}`)
	// Internal error: a server error.
	send(tr, ClientToServer, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	send(tr, ServerToClient, `{"jsonrpc":"2.0","id":2,"error":{"code":-32603,"message":"boom"}}`)
	// Upstream trace context in _meta becomes the parent and is not overwritten.
	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	out := send(tr, ClientToServer, `{"jsonrpc":"2.0","id":3,"method":"ping","params":{"_meta":{"traceparent":"`+tp+`"}}}`)
	send(tr, ServerToClient, `{"jsonrpc":"2.0","id":3,"result":{}}`)
	// Cancelled request.
	send(tr, ClientToServer, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"slow"}}`)
	send(tr, ClientToServer, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":4}}`)

	got := spans.GetSpans()
	if len(got) != 4 {
		t.Fatalf("want 4 spans, got %d", len(got))
	}
	if a := attrs(got[0]); a["error.type"] != "" || a["rpc.response.status_code"] != "-32602" || got[0].Status.Code == codes.Error {
		t.Errorf("-32602 misclassified: %v status=%v", a, got[0].Status)
	}
	if a := attrs(got[1]); a["error.type"] != "-32603" || got[1].Status.Description != "boom" {
		t.Errorf("-32603 misclassified: %v status=%v", a, got[1].Status)
	}
	if got[2].Parent.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" || !strings.Contains(out, tp) {
		t.Errorf("_meta parent not honoured: parent=%v out=%s", got[2].Parent, out)
	}
	if a := attrs(got[3]); a["error.type"] != "cancelled" {
		t.Errorf("cancel not recorded: %v", a)
	}
}

func BenchmarkRequestResponse(b *testing.B) {
	otel.SetTracerProvider(sdktrace.NewTracerProvider()) // no exporter: measure our own cost
	otel.SetTextMapPropagator(propagation.TraceContext{})
	tr := NewTracker(Options{Transport: "tcp", InjectContext: true})
	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_weather","arguments":{"city":"Tunis"}}}`)
	resp := []byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"sunny"}]}}`)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		tr.Frame(ctx, ClientToServer, "s", "", req)
		tr.Frame(ctx, ServerToClient, "s", "", resp)
	}
}

// Issue #8: redaction by key, by value shape, and the capture allowlist.
func TestRedaction(t *testing.T) {
	tr := NewTracker(Options{CapturePayloads: true})
	for in, want := range map[string]string{
		`{"city":"Tunis","api_key":"x"}`:                                                 `{"api_key":"[REDACTED]","city":"Tunis"}`,
		`{"nested":[{"Password":"x"}]}`:                                                  `{"nested":[{"Password":"[REDACTED]"}]}`,
		`{"note":"use sk-abcdefghijklmnopqrstu please"}`:                                 `{"note":"use [REDACTED] please"}`,
		`{"h":"Bearer abc.def-ghi_jkl"}`:                                                 `{"h":"[REDACTED]"}`,
		`{"jwt":"eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0.SflKxwRJSMeKKF2QT4"}`:                 `{"jwt":"[REDACTED]"}`,
		`{"aws":"AKIAIOSFODNN7EXAMPLE","gh":"ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`: `{"aws":"[REDACTED]","gh":"[REDACTED]"}`,
		`{"msg":"skeleton sk-short monkey"}`:                                             `{"msg":"skeleton sk-short monkey"}`,
		`not json with sk-abcdefghijklmnopqrstu`:                                         `not json with [REDACTED]`,
	} {
		if got := tr.redact([]byte(in)); got != want {
			t.Errorf("redact(%s)\n got %s\nwant %s", in, got, want)
		}
	}

	custom := NewTracker(Options{CapturePayloads: true, RedactKeys: regexp.MustCompile(`^ssn$`)})
	if got := custom.redact([]byte(`{"ssn":"1","password":"2"}`)); got != `{"password":"2","ssn":"[REDACTED]"}` {
		t.Errorf("custom keys replace the default: %s", got)
	}
}

func TestCaptureAllowlist(t *testing.T) {
	_, spans, _ := setup(t)
	tr := NewTracker(Options{Transport: "pipe", CapturePayloads: true, CaptureTools: map[string]bool{"search": true}})
	for i, tool := range []string{"search", "delete_repo"} {
		id := strconv.Itoa(i + 1)
		send(tr, ClientToServer, `{"jsonrpc":"2.0","id":`+id+`,"method":"tools/call","params":{"name":"`+tool+`","arguments":{"q":"x"}}}`)
		send(tr, ServerToClient, `{"jsonrpc":"2.0","id":`+id+`,"result":{"content":[]}}`)
	}
	got := spans.GetSpans()
	if a := attrs(got[0]); a["gen_ai.tool.call.arguments"] == "" || a["gen_ai.tool.call.result"] == "" {
		t.Errorf("allowlisted tool should be captured: %v", a)
	}
	if a := attrs(got[1]); a["gen_ai.tool.call.arguments"] != "" || a["gen_ai.tool.call.result"] != "" {
		t.Errorf("other tools must not be captured: %v", a)
	}
}

// Issue #6: MCP Tasks (2025-11-25) messages are tied back to the request
// that created the task, and the task's own duration is recorded once.
func TestTasks(t *testing.T) {
	tr, spans, reader := setup(t)
	const T, U = "786512e2-9e0d-44bd-8f29-789f320fe840", "task-2"
	rt := `"_meta":{"io.modelcontextprotocol/related-task":{"taskId":"` + T + `"}}`

	send(tr, ClientToServer, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_weather","arguments":{"city":"NY"},"task":{"ttl":60000}}}`)
	send(tr, ServerToClient, `{"jsonrpc":"2.0","id":1,"result":{"task":{"taskId":"`+T+`","status":"working","createdAt":"2025-11-25T10:30:00Z"}}}`)
	send(tr, ClientToServer, `{"jsonrpc":"2.0","id":2,"method":"tasks/get","params":{"taskId":"`+T+`"}}`)
	send(tr, ServerToClient, `{"jsonrpc":"2.0","id":2,"result":{"taskId":"`+T+`","status":"input_required"}}`)
	send(tr, ServerToClient, `{"jsonrpc":"2.0","id":"e1","method":"elicitation/create","params":{"message":"which NY?",`+rt+`}}`)
	send(tr, ClientToServer, `{"jsonrpc":"2.0","id":"e1","result":{"action":"accept",`+rt+`}}`)
	send(tr, ClientToServer, `{"jsonrpc":"2.0","id":3,"method":"tasks/result","params":{"taskId":"`+T+`"}}`)
	send(tr, ServerToClient, `{"jsonrpc":"2.0","id":3,"result":{"content":[],"isError":false,`+rt+`}}`)

	// Second task fails via a status notification; a later tasks/result must not double count.
	send(tr, ClientToServer, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_weather","task":{}}}`)
	send(tr, ServerToClient, `{"jsonrpc":"2.0","id":4,"result":{"task":{"taskId":"`+U+`","status":"working"}}}`)
	send(tr, ServerToClient, `{"jsonrpc":"2.0","method":"notifications/tasks/status","params":{"taskId":"`+U+`","status":"failed"}}`)
	send(tr, ClientToServer, `{"jsonrpc":"2.0","id":5,"method":"tasks/result","params":{"taskId":"`+U+`"}}`)
	send(tr, ServerToClient, `{"jsonrpc":"2.0","id":5,"result":{"content":[],"isError":true}}`)
	// Unknown task: attribute only, no link, no crash.
	send(tr, ClientToServer, `{"jsonrpc":"2.0","id":6,"method":"tasks/get","params":{"taskId":"nope"}}`)
	send(tr, ServerToClient, `{"jsonrpc":"2.0","id":6,"error":{"code":-32602,"message":"Task not found"}}`)

	got := spans.GetSpans()
	if len(got) != 7 {
		t.Fatalf("want 7 spans, got %d", len(got))
	}
	origin := got[0]
	if a := attrs(origin); a["mcp.task.id"] != T || a["mcp.task.status"] != "working" {
		t.Errorf("creating span should carry the task: %v", a)
	}
	linked := func(s tracetest.SpanStub) bool {
		return len(s.Links) == 1 && s.Links[0].SpanContext.SpanID() == origin.SpanContext.SpanID()
	}
	for _, i := range []int{1, 2, 3} { // tasks/get, elicitation/create, tasks/result
		if a := attrs(got[i]); a["mcp.task.id"] != T || !linked(got[i]) {
			t.Errorf("%s: want mcp.task.id and a link to the creating span; attrs=%v links=%d", got[i].Name, a, len(got[i].Links))
		}
	}
	if a := attrs(got[1]); a["mcp.task.status"] != "input_required" {
		t.Errorf("tasks/get should record the polled status: %v", a)
	}
	if got[2].SpanKind != trace.SpanKindClient {
		t.Errorf("server-initiated elicitation should be a CLIENT span")
	}
	if a := attrs(got[6]); a["mcp.task.id"] != "nope" || len(got[6].Links) != 0 {
		t.Errorf("unknown task: %v links=%d", a, len(got[6].Links))
	}

	var rm metricdata.ResourceMetrics
	reader.Collect(context.Background(), &rm)
	counts := map[string]uint64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "mcp.task.duration" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Histogram[float64]).DataPoints {
				st, _ := dp.Attributes.Value("mcp.task.status")
				tool, _ := dp.Attributes.Value("gen_ai.tool.name")
				if tool.AsString() != "get_weather" {
					t.Errorf("task metric should carry the origin's tool, got %q", tool.AsString())
				}
				if _, has := dp.Attributes.Value("mcp.task.id"); has {
					t.Error("task id must never be a metric attribute")
				}
				counts[st.AsString()] += dp.Count
			}
		}
	}
	if counts["completed"] != 1 || counts["failed"] != 1 || len(counts) != 2 {
		t.Errorf("want one completed and one failed task, got %v", counts)
	}
}

// Issue #13: observing from the agent's side flips span kinds, metric names
// and the semconv error rules.
func TestClientRole(t *testing.T) {
	_, spans, reader := setup(t)
	tr := NewTracker(Options{Transport: "tcp", Role: "client", ServerAddress: "mcp.example.com", ServerPort: 443})

	send(tr, ClientToServer, `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`)
	send(tr, ServerToClient, `{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":"2025-11-25","serverInfo":{"name":"remote"}}}`)
	send(tr, ClientToServer, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search"}}`)
	send(tr, ServerToClient, `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"no such tool"}}`)
	send(tr, ServerToClient, `{"jsonrpc":"2.0","id":"s1","method":"sampling/createMessage","params":{}}`)
	send(tr, ClientToServer, `{"jsonrpc":"2.0","id":"s1","error":{"code":-32602,"message":"bad params"}}`)
	tr.EndSession("")

	got := spans.GetSpans()
	if len(got) != 3 {
		t.Fatalf("want 3 spans, got %d", len(got))
	}
	call, sampling := got[1], got[2]
	if a := attrs(call); call.SpanKind != trace.SpanKindClient || a["server.address"] != "mcp.example.com" || a["server.port"] != "443" {
		t.Errorf("agent-sent request: kind=%v attrs=%v", call.SpanKind, a)
	}
	if a := attrs(call); a["error.type"] != "-32601" {
		t.Errorf("on the client every JSON-RPC error code is an error, got error.type=%q", a["error.type"])
	}
	if a := attrs(sampling); sampling.SpanKind != trace.SpanKindServer || a["error.type"] != "" || a["server.address"] != "" {
		t.Errorf("request received by the agent should be a SERVER span with server-side error rules: kind=%v attrs=%v", sampling.SpanKind, a)
	}

	var rm metricdata.ResourceMetrics
	reader.Collect(context.Background(), &rm)
	names := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names[m.Name] = true
		}
	}
	for _, want := range []string{"mcp.client.operation.duration", "mcp.server.operation.duration", "mcp.client.session.duration", "mcp.client.sessions.active", "mcp.client.requests.inflight"} {
		if !names[want] {
			t.Errorf("missing metric %s (have %v)", want, names)
		}
	}
	for _, unwanted := range []string{"mcp.server.session.duration", "mcp.server.sessions.active"} {
		if names[unwanted] {
			t.Errorf("client role must not emit %s", unwanted)
		}
	}
}

// Issue #50: a server that re-serialises 1.0 as 1 must still close the span.
func TestIDCanonicalCorrelation(t *testing.T) {
	tr, spans, _ := setup(t)
	send(tr, ClientToServer, `{"jsonrpc":"2.0","id":1.0,"method":"tools/call","params":{"name":"x"}}`)
	send(tr, ServerToClient, `{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`)
	send(tr, ClientToServer, `{"jsonrpc":"2.0","id":"7","method":"ping"}`)
	send(tr, ServerToClient, `{"jsonrpc":"2.0","id":7,"result":{}}`) // number 7 is not string "7"
	send(tr, ClientToServer, `{"jsonrpc":"2.0","id":2e0,"method":"tools/call","params":{"name":"slow"}}`)
	send(tr, ClientToServer, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2}}`)

	got := spans.GetSpans()
	if len(got) != 2 {
		t.Fatalf("want 2 ended spans (1.0/1 matched, 2e0 cancelled via 2; \"7\" still open), got %d", len(got))
	}
	if a := attrs(got[0]); got[0].Name != "tools/call x" || a["jsonrpc.request.id"] != "1" || a["error.type"] != "" {
		t.Errorf("1.0 vs 1: name=%q attrs=%v", got[0].Name, a)
	}
	if a := attrs(got[1]); a["error.type"] != "cancelled" {
		t.Errorf("cancel with requestId 2 should end the 2e0 request: %v", a)
	}
}
