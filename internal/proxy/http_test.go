package proxy

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/firasmosbehi/mcp-exporter/internal/observe"
)

func TestHTTPProxyJSONAndSSE(t *testing.T) {
	spans := tracetest.NewInMemoryExporter()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans)))
	otel.SetTextMapPropagator(propagation.TraceContext{})

	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		switch {
		case strings.Contains(gotBody, `"initialize"`):
			w.Header().Set("Mcp-Session-Id", "sess-1")
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":"2025-11-25","serverInfo":{"name":"up"}}}`)
		default: // SSE: a log notification, then the response split over two data lines
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "event: message\r\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{\"level\":\"info\",\"data\":\"hi\"}}\r\n\r\n")
			io.WriteString(w, "id: 1\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\ndata: \"result\":{\"content\":[]}}\n\n")
		}
	}))
	defer upstream.Close()
	up, _ := url.Parse(upstream.URL)
	tr := observe.NewTracker(observe.Options{Transport: "tcp", InjectContext: true})
	proxy := httptest.NewServer(HTTP(tr, up))
	defer proxy.Close()

	post := func(sess, body string) *http.Response {
		req, _ := http.NewRequest("POST", proxy.URL+"/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if sess != "" {
			req.Header.Set("Mcp-Session-Id", sess)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}

	resp := post("", `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`)
	sess := resp.Header.Get("Mcp-Session-Id")
	if sess != "sess-1" {
		t.Fatalf("session header not passed through: %q", sess)
	}
	post(sess, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo"}}`)
	if !strings.Contains(gotBody, "traceparent") {
		t.Errorf("upstream did not receive injected trace context: %s", gotBody)
	}

	got := spans.GetSpans()
	if len(got) != 2 {
		t.Fatalf("want 2 spans (initialize, tools/call), got %d", len(got))
	}
	if got[1].Name != "tools/call echo" {
		t.Errorf("SSE response not correlated: %q", got[1].Name)
	}
	for _, kv := range got[1].Attributes {
		if kv.Key == "mcp.protocol.version" && kv.Value.AsString() != "2025-11-25" {
			t.Errorf("session protocol version not applied: %v", kv.Value)
		}
	}
}

// Issue #4: compressed traffic must still be observed and stay valid.
func TestHTTPProxyCompression(t *testing.T) {
	spans := tracetest.NewInMemoryExporter()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans)))
	otel.SetTextMapPropagator(propagation.TraceContext{})

	var gotBody, gotEncoding string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Content-Encoding")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		resp := `{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") { // compress whenever allowed
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			io.WriteString(zw, resp)
			zw.Close()
			return
		}
		io.WriteString(w, resp)
	}))
	defer upstream.Close()
	up, _ := url.Parse(upstream.URL)
	proxy := httptest.NewServer(HTTP(observe.NewTracker(observe.Options{Transport: "tcp", InjectContext: true}), up))
	defer proxy.Close()

	var zbody bytes.Buffer
	zw := gzip.NewWriter(&zbody)
	io.WriteString(zw, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo"}}`)
	zw.Close()
	req, _ := http.NewRequest("POST", proxy.URL+"/mcp", &zbody)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Accept-Encoding", "gzip")
	// Raw transport: no transparent decompression, so we see exactly what the client gets.
	resp, err := (&http.Transport{DisableCompression: true}).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if gotEncoding != "" || !strings.Contains(gotBody, `"traceparent"`) {
		t.Errorf("upstream should get a decompressed, observed body; encoding=%q body=%s", gotEncoding, gotBody)
	}
	if !strings.HasPrefix(string(body), `{"jsonrpc"`) {
		t.Errorf("client should get a plain JSON response, got %q (Content-Encoding %q)", body, resp.Header.Get("Content-Encoding"))
	}
	if got := spans.GetSpans(); len(got) != 1 || got[0].Name != "tools/call echo" {
		t.Fatalf("want one tools/call echo span, got %d", len(got))
	}
}

func TestGunzipLimit(t *testing.T) {
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	zw.Write(make([]byte, maxObserved+1)) // ~16 MiB of zeros compresses to ~16 KiB
	zw.Close()
	if _, err := gunzip(b.Bytes()); err != errTooBig {
		t.Fatalf("want errTooBig, got %v", err)
	}
}
