package proxy

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

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

// Issue #5: /readyz follows the upstream, /healthz does not.
func TestReadyzFollowsUpstream(t *testing.T) {
	readyTTL = 0
	defer func() { readyTTL = 2 * time.Second }()
	upstream := httptest.NewServer(http.NotFoundHandler())
	up, _ := url.Parse(upstream.URL)
	proxy := httptest.NewServer(HTTP(observe.NewTracker(observe.Options{Transport: "tcp"}), up))
	defer proxy.Close()

	code := func(path string) int {
		resp, err := http.Get(proxy.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := code("/readyz"); c != 200 {
		t.Fatalf("readyz with live upstream = %d", c)
	}
	upstream.Close()
	if c := code("/readyz"); c != 503 {
		t.Errorf("readyz with dead upstream = %d, want 503", c)
	}
	if c := code("/healthz"); c != 200 {
		t.Errorf("healthz must not depend on upstream, got %d", c)
	}
}

func TestHostPort(t *testing.T) {
	for in, want := range map[string]string{
		"http://a:3000": "a:3000", "http://a": "a:80", "https://a": "a:443", "http://[::1]": "[::1]:80",
	} {
		u, _ := url.Parse(in)
		if got := hostPort(u); got != want {
			t.Errorf("hostPort(%s) = %s, want %s", in, got, want)
		}
	}
}

// Issue #7: HTTP paths beyond "one POST, one response on the same stream".
func TestHTTPProxySessionFlows(t *testing.T) {
	spans := tracetest.NewInMemoryExporter()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans)))
	otel.SetTextMapPropagator(propagation.TraceContext{})

	sse := func(w http.ResponseWriter, events ...string) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i, e := range events {
			io.WriteString(w, "id: "+strconv.Itoa(i)+"\ndata: "+e+"\n\n")
		}
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body := string(b)
		switch {
		case r.Method == http.MethodGet && r.Header.Get("Last-Event-ID") != "":
			// Resumed stream redelivers the response to request 3.
			sse(w, `{"jsonrpc":"2.0","id":3,"result":{"content":[]}}`)
		case r.Method == http.MethodGet:
			// Standalone stream: the server asks the client to sample.
			sse(w, `{"jsonrpc":"2.0","id":"srv-1","method":"sampling/createMessage","params":{"maxTokens":5}}`)
		case strings.HasPrefix(strings.TrimSpace(body), "["): // batch
			sse(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`, `{"jsonrpc":"2.0","id":2,"result":{"content":[]}}`)
		default: // responses, notifications, and requests answered later
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer upstream.Close()
	up, _ := url.Parse(upstream.URL)
	proxy := httptest.NewServer(HTTP(observe.NewTracker(observe.Options{Transport: "tcp"}), up))
	defer proxy.Close()

	do := func(method, body string, hdr ...string) {
		req, _ := http.NewRequest(method, proxy.URL+"/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Mcp-Session-Id", "s1")
		for i := 0; i < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	do("POST", `[{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"a"}},{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"b"}}]`)
	do("GET", "")
	do("POST", `{"jsonrpc":"2.0","id":"srv-1","result":{"model":"m","role":"assistant","content":{"type":"text","text":"hi"}}}`)
	do("POST", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"slow"}}`)
	do("GET", "", "Last-Event-ID", "7")
	do("POST", `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"abandoned"}}`)
	do("POST", `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":4}}`)

	byName := map[string]tracetest.SpanStub{}
	for _, s := range spans.GetSpans() {
		byName[s.Name] = s
	}
	for _, name := range []string{"tools/call a", "tools/call b", "sampling/createMessage", "tools/call slow", "tools/call abandoned"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("missing span %q (have %d spans)", name, len(byName))
		}
	}
	if s := byName["sampling/createMessage"]; s.SpanKind != trace.SpanKindClient {
		t.Errorf("server-initiated request should be a CLIENT span, got %v", s.SpanKind)
	}
	errType := func(s tracetest.SpanStub) string {
		for _, kv := range s.Attributes {
			if kv.Key == "error.type" {
				return kv.Value.AsString()
			}
		}
		return ""
	}
	if e := errType(byName["tools/call abandoned"]); e != "cancelled" {
		t.Errorf("cancelled request error.type = %q", e)
	}
	if e := errType(byName["tools/call slow"]); e != "" {
		t.Errorf("request answered on a resumed stream should succeed, error.type = %q", e)
	}
}

// Issue #54: a gateway routes /NAME/... to each upstream with the prefix
// stripped, keeps correlation per route (both servers may hand out the same
// session id and use the same request ids), and aggregates readiness.
func TestGateway(t *testing.T) {
	readyTTL = 0
	defer func() { readyTTL = 2 * time.Second }()
	spans := tracetest.NewInMemoryExporter()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans)))

	var mu sync.Mutex
	paths := map[string]string{}
	server := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths[name] = r.URL.Path
			mu.Unlock()
			time.Sleep(10 * time.Millisecond) // overlap the two requests
			w.Header().Set("Mcp-Session-Id", "same")
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
		}))
	}
	a, b := server("a"), server("b")
	defer a.Close()
	var routes []Route
	for name, s := range map[string]*httptest.Server{"a": a, "b": b} {
		up, _ := url.Parse(s.URL)
		routes = append(routes, Route{Name: name, Upstream: up, Tracker: observe.NewTracker(observe.Options{Transport: "tcp"})})
	}
	gw := httptest.NewServer(Gateway(routes))
	defer gw.Close()

	done := make(chan struct{})
	for _, name := range []string{"a", "b"} {
		go func() {
			defer func() { done <- struct{}{} }()
			req, _ := http.NewRequest("POST", gw.URL+"/"+name+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`"}}`))
			req.Header.Set("Mcp-Session-Id", "same")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}()
	}
	<-done
	<-done
	if paths["a"] != "/mcp" || paths["b"] != "/mcp" {
		t.Errorf("prefix not stripped: %v", paths)
	}
	got := map[string]bool{}
	for _, s := range spans.GetSpans() {
		got[s.Name] = true
	}
	if !got["tools/call a"] || !got["tools/call b"] || len(spans.GetSpans()) != 2 {
		t.Errorf("want one span per route, got %v", got)
	}

	code := func(path string) int {
		resp, err := http.Get(gw.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := code("/readyz"); c != 200 {
		t.Fatalf("aggregate readyz with live upstreams = %d", c)
	}
	b.Close()
	for path, want := range map[string]int{"/readyz": 503, "/a/readyz": 200, "/b/readyz": 503, "/healthz": 200} {
		if c := code(path); c != want {
			t.Errorf("%s = %d, want %d", path, c, want)
		}
	}
}

// Legacy HTTP+SSE (2024-11-05): responses arrive on the GET stream, not on
// the POST, and the session lives in the endpoint's query. They must still
// correlate, and closing the stream ends the session.
func TestLegacySSE(t *testing.T) {
	spans := tracetest.NewInMemoryExporter()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans)))

	stream := make(chan string, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "event: endpoint\ndata: /message?sessionId=abc\n\n")
			w.(http.Flusher).Flush()
			for msg := range stream {
				io.WriteString(w, "event: message\ndata: "+msg+"\n\n")
				w.(http.Flusher).Flush()
			}
		case http.MethodPost:
			if r.URL.Query().Get("sessionId") != "abc" {
				t.Errorf("POST lost the session query: %s", r.URL)
			}
			b, _ := io.ReadAll(r.Body)
			id := "0"
			if strings.Contains(string(b), `"id":1`) {
				id = "1"
			}
			w.WriteHeader(http.StatusAccepted)
			stream <- `{"jsonrpc":"2.0","id":` + id + `,"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"legacy"}}}`
		}
	}))
	defer upstream.Close()
	up, _ := url.Parse(upstream.URL)
	proxy := httptest.NewServer(HTTP(observe.NewTracker(observe.Options{Transport: "tcp"}), up))
	defer proxy.Close()

	get, err := http.Get(proxy.URL + "/sse")
	if err != nil {
		t.Fatal(err)
	}
	rd := bufio.NewReader(get.Body)
	readEvent := func() string {
		var data string
		for {
			l, err := rd.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if l == "\n" {
				return data
			}
			if d, ok := strings.CutPrefix(strings.TrimSuffix(l, "\n"), "data: "); ok {
				data = d
			}
		}
	}
	ep := readEvent()
	for i, body := range []string{
		`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo"}}`,
	} {
		resp, err := http.Post(proxy.URL+ep, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := readEvent(); !strings.Contains(got, `"id":`+strconv.Itoa(i)) {
			t.Fatalf("event %d = %s", i, got)
		}
	}
	close(stream)
	get.Body.Close()

	got := spans.GetSpans()
	if len(got) != 2 {
		t.Fatalf("want 2 spans, got %d", len(got))
	}
	for _, s := range got {
		if s.Status.Code != 0 {
			t.Errorf("%s: status %v %q, want a matched response", s.Name, s.Status.Code, s.Status.Description)
		}
		for _, kv := range s.Attributes {
			if kv.Key == "mcp.session.id" && kv.Value.AsString() == "" {
				t.Errorf("%s: empty mcp.session.id", s.Name)
			}
		}
	}
	if got[1].Name != "tools/call echo" {
		t.Errorf("second span = %q", got[1].Name)
	}
}
