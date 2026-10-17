package observe

import (
	"context"
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
