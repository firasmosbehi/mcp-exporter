package observe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"regexp"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/firasmosbehi/mcp-exporter/internal/jsonrpc"
)

const scopeName = "github.com/firasmosbehi/mcp-exporter"

// Dir is the direction a message travels.
type Dir int

const (
	ClientToServer Dir = iota
	ServerToClient
)

func (d Dir) other() Dir { return 1 - d }

type Options struct {
	Transport       string          // network.transport: "pipe" (stdio) or "tcp" (HTTP)
	InjectContext   bool            // write traceparent into params._meta of client requests lacking one
	CapturePayloads bool            // record redacted tool arguments/results (opt-in per semconv)
	MaxPayload      int             // truncate captured payloads to this many bytes
	CaptureTools    map[string]bool // if non-empty, capture payloads only for these tools
	RedactKeys      *regexp.Regexp  // object keys whose values are redacted; nil means DefaultRedactKeys
	MaxNames        int             // distinct tool/prompt names on metrics before folding into "_other"
}

// Semconv: on the server these JSON-RPC codes are caller mistakes, not server errors.
var serverNonErrors = map[int]bool{-32700: true, -32600: true, -32601: true, -32602: true, -32002: true}

// Tracker turns a stream of JSON-RPC frames into spans, metrics and logs.
// It is safe for concurrent use.
type Tracker struct {
	opts   Options
	tracer trace.Tracer
	log    *slog.Logger
	prop   propagation.TextMapPropagator

	serverDur, clientDur, sessDur metric.Float64Histogram
	msgSize                       metric.Int64Histogram
	inflight, sessActive          metric.Int64UpDownCounter
	parseErrs                     metric.Int64Counter

	mu       sync.Mutex
	pending  map[pendingKey]*pending
	sessions map[string]*session
	names    map[string]bool
}

type pendingKey struct {
	scope string
	dir   Dir
	id    string
}

type pending struct {
	ctx        context.Context
	span       trace.Span
	start      time.Time
	dir        Dir
	method     string
	tool       string
	clientName string
	attrs      []attribute.KeyValue     // metric attributes
	base       metric.MeasurementOption // attributes at start, for the inflight counter
}

type session struct {
	start, lastSeen time.Time
	protocol        string
	serverName      string
	clientName      string
}

// Semconv-advised buckets for MCP durations, in seconds.
var durBuckets = []float64{0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 30, 60, 120, 300}

func NewTracker(opts Options) *Tracker {
	if opts.RedactKeys == nil {
		opts.RedactKeys = regexp.MustCompile(DefaultRedactKeys)
	}
	if opts.MaxPayload <= 0 {
		opts.MaxPayload = 4096
	}
	if opts.MaxNames <= 0 {
		opts.MaxNames = 500
	}
	m := otel.Meter(scopeName)
	t := &Tracker{
		opts:     opts,
		tracer:   otel.Tracer(scopeName),
		log:      otelslog.NewLogger(scopeName),
		prop:     otel.GetTextMapPropagator(),
		pending:  map[pendingKey]*pending{},
		sessions: map[string]*session{},
		names:    map[string]bool{},
	}
	// Instrument creation only fails on invalid names; ours are constants.
	t.serverDur, _ = m.Float64Histogram("mcp.server.operation.duration", metric.WithUnit("s"),
		metric.WithDescription("Duration of MCP requests handled by the server."), metric.WithExplicitBucketBoundaries(durBuckets...))
	t.clientDur, _ = m.Float64Histogram("mcp.client.operation.duration", metric.WithUnit("s"),
		metric.WithDescription("Duration of server-initiated MCP requests (sampling, elicitation, roots)."), metric.WithExplicitBucketBoundaries(durBuckets...))
	t.sessDur, _ = m.Float64Histogram("mcp.server.session.duration", metric.WithUnit("s"),
		metric.WithDescription("Duration of MCP sessions."), metric.WithExplicitBucketBoundaries(durBuckets...))
	t.msgSize, _ = m.Int64Histogram("mcp.message.size", metric.WithUnit("By"),
		metric.WithDescription("Size of JSON-RPC frames."))
	t.inflight, _ = m.Int64UpDownCounter("mcp.server.requests.inflight", metric.WithUnit("{request}"))
	t.sessActive, _ = m.Int64UpDownCounter("mcp.server.sessions.active", metric.WithUnit("{session}"))
	t.parseErrs, _ = m.Int64Counter("mcp_exporter.parse.errors", metric.WithUnit("{frame}"),
		metric.WithDescription("Frames that were not valid JSON-RPC and were passed through unobserved."))
	return t
}

// Frame observes one transport frame (a message or batch) and returns the
// bytes to forward. scope groups request ids that share an id space (one stdio
// pipe, one HTTP session or one sessionless HTTP exchange); session is the MCP
// session id, if any. ctx may carry a transport-level span context.
func (t *Tracker) Frame(ctx context.Context, dir Dir, scope, session string, raw []byte) []byte {
	dirAttr := attribute.String("mcp.direction", map[Dir]string{ClientToServer: "client_to_server", ServerToClient: "server_to_client"}[dir])
	t.msgSize.Record(ctx, int64(len(raw)), metric.WithAttributes(dirAttr, attribute.String("network.transport", t.opts.Transport)))

	msgs, batch, err := jsonrpc.Parse(raw)
	if err != nil {
		t.parseErrs.Add(ctx, 1)
		return raw
	}
	t.touch(session)
	changed := false
	for i := range msgs {
		m := &msgs[i]
		switch {
		case m.IsRequest():
			changed = t.startRequest(ctx, dir, scope, session, m) || changed
		case m.IsResponse():
			t.endRequest(pendingKey{scope, dir.other(), string(m.ID)}, session, m, "")
		case m.IsNotification():
			t.notification(ctx, dir, scope, m)
		}
	}
	if !changed {
		return raw
	}
	out, err := jsonrpc.Encode(msgs, batch)
	if err != nil {
		return raw
	}
	return out
}

func (t *Tracker) startRequest(ctx context.Context, dir Dir, scope, sess string, m *jsonrpc.Message) (changed bool) {
	p := m.ParseParams()

	// Semconv: params._meta context is the parent; ambient transport context is a link.
	parent, opts := ctx, []trace.SpanStartOption{}
	if p.Meta["traceparent"] != "" {
		parent = t.prop.Extract(context.Background(), propagation.MapCarrier(p.Meta))
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
			opts = append(opts, trace.WithLinks(trace.Link{SpanContext: sc}))
		}
	}
	kind := trace.SpanKindServer
	if dir == ServerToClient {
		kind = trace.SpanKindClient
	}

	spanAttrs := []attribute.KeyValue{
		attribute.String("mcp.method.name", m.Method),
		attribute.String("jsonrpc.request.id", idString(m.ID)),
		attribute.String("network.transport", t.opts.Transport),
	}
	metricAttrs := []attribute.KeyValue{
		attribute.String("mcp.method.name", m.Method),
		attribute.String("network.transport", t.opts.Transport),
	}
	if sess != "" {
		spanAttrs = append(spanAttrs, attribute.String("mcp.session.id", hashID(sess)))
	}
	t.mu.Lock()
	if s := t.sessions[sess]; s != nil {
		if s.protocol != "" {
			spanAttrs = append(spanAttrs, attribute.String("mcp.protocol.version", s.protocol))
			metricAttrs = append(metricAttrs, attribute.String("mcp.protocol.version", s.protocol))
		}
		if s.serverName != "" {
			spanAttrs = append(spanAttrs, attribute.String("mcp.server.name", s.serverName))
		}
		if s.clientName != "" {
			spanAttrs = append(spanAttrs, attribute.String("mcp.client.name", s.clientName))
		}
	}
	t.mu.Unlock()

	name := m.Method
	switch m.Method {
	case "tools/call":
		name += " " + p.Name
		spanAttrs = append(spanAttrs, attribute.String("gen_ai.tool.name", p.Name), attribute.String("gen_ai.operation.name", "execute_tool"))
		metricAttrs = append(metricAttrs, attribute.String("gen_ai.tool.name", t.capName("tool:"+p.Name, p.Name)))
		if t.capture(p.Name) && len(p.Arguments) > 0 {
			spanAttrs = append(spanAttrs, attribute.String("gen_ai.tool.call.arguments", t.redact(p.Arguments)))
		}
	case "prompts/get":
		name += " " + p.Name
		spanAttrs = append(spanAttrs, attribute.String("gen_ai.prompt.name", p.Name))
		metricAttrs = append(metricAttrs, attribute.String("gen_ai.prompt.name", t.capName("prompt:"+p.Name, p.Name)))
	case "resources/read", "resources/subscribe", "resources/unsubscribe":
		spanAttrs = append(spanAttrs, attribute.String("mcp.resource.uri", p.URI))
	}

	opts = append(opts, trace.WithSpanKind(kind), trace.WithAttributes(spanAttrs...))
	spanCtx, span := t.tracer.Start(parent, name, opts...)

	pd := &pending{ctx: spanCtx, span: span, start: time.Now(), dir: dir, method: m.Method, tool: p.Name,
		attrs: metricAttrs, base: metric.WithAttributes(metricAttrs...)}
	if p.ClientInfo != nil {
		pd.clientName = p.ClientInfo.Name
	}
	key := pendingKey{scope, dir, string(m.ID)}
	t.mu.Lock()
	old := t.pending[key]
	t.pending[key] = pd
	t.mu.Unlock()
	if old != nil { // id reused before a response: close the orphan
		t.finish(old, sess, nil, "duplicate_id")
	}
	if dir == ClientToServer {
		t.inflight.Add(spanCtx, 1, pd.base)
	}

	if t.opts.InjectContext && dir == ClientToServer && p.Meta["traceparent"] == "" {
		carrier := propagation.MapCarrier{}
		t.prop.Inject(spanCtx, carrier)
		if len(carrier) > 0 {
			m.SetMeta(carrier)
			return true
		}
	}
	return false
}

func (t *Tracker) endRequest(key pendingKey, sess string, m *jsonrpc.Message, errType string) {
	t.mu.Lock()
	pd := t.pending[key]
	delete(t.pending, key)
	t.mu.Unlock()
	if pd != nil {
		t.finish(pd, sess, m, errType)
	}
}

// finish ends a pending request. m is the response, or nil when the request
// ended some other way (errType says how).
func (t *Tracker) finish(pd *pending, sess string, m *jsonrpc.Message, errType string) {
	span := pd.span
	if m != nil && m.Error != nil {
		code := strconv.Itoa(m.Error.Code)
		span.SetAttributes(attribute.String("rpc.response.status_code", code))
		pd.attrs = append(pd.attrs, attribute.String("rpc.response.status_code", code))
		if pd.dir == ServerToClient || !serverNonErrors[m.Error.Code] {
			errType = code
			span.SetStatus(codes.Error, m.Error.Message)
		}
	} else if m != nil {
		r := m.ParseResult()
		if r.IsError {
			errType = "tool_error"
		}
		if pd.method == "initialize" {
			t.startSession(sess, r, pd.clientName)
		}
		if pd.method == "tools/call" && t.capture(pd.tool) {
			span.SetAttributes(attribute.String("gen_ai.tool.call.result", t.redact(m.Result)))
		}
	}
	if errType != "" {
		span.SetAttributes(attribute.String("error.type", errType))
		pd.attrs = append(pd.attrs, attribute.String("error.type", errType))
		if m == nil || m.Error == nil {
			span.SetStatus(codes.Error, errType)
		}
	}
	span.End()

	dur := time.Since(pd.start)
	attrs := metric.WithAttributes(pd.attrs...)
	if pd.dir == ClientToServer {
		t.serverDur.Record(pd.ctx, dur.Seconds(), attrs)
		t.inflight.Add(pd.ctx, -1, pd.base)
	} else {
		t.clientDur.Record(pd.ctx, dur.Seconds(), attrs)
	}
	logAttrs := []any{"mcp.method.name", pd.method, "duration_ms", float64(dur.Microseconds()) / 1000}
	if errType != "" {
		logAttrs = append(logAttrs, "error.type", errType)
	}
	t.log.InfoContext(pd.ctx, "mcp request", logAttrs...)
}

func (t *Tracker) notification(ctx context.Context, dir Dir, scope string, m *jsonrpc.Message) {
	p := m.ParseParams()
	switch m.Method {
	case "notifications/cancelled":
		// Sent by whoever issued the request, so the request travelled the same way.
		t.endRequest(pendingKey{scope, dir, string(p.RequestID)}, "", nil, "cancelled")
	case "notifications/message":
		if dir == ServerToClient {
			t.log.Log(ctx, mcpLevel(p.Level), "mcp server log",
				"mcp.log.level", p.Level,
				"mcp.log.logger", p.Logger,
				"mcp.log.data", t.redact(p.Data))
		}
	}
}

func (t *Tracker) startSession(sess string, r jsonrpc.Result, clientName string) {
	t.mu.Lock()
	s := t.sessions[sess]
	isNew := s == nil
	if isNew {
		now := time.Now()
		s = &session{start: now, lastSeen: now}
		t.sessions[sess] = s
	}
	s.protocol = r.ProtocolVersion
	if r.ServerInfo != nil {
		s.serverName = r.ServerInfo.Name
	}
	s.clientName = clientName
	t.mu.Unlock()
	if isNew {
		t.sessActive.Add(context.Background(), 1)
	}
}

func (t *Tracker) touch(sess string) {
	t.mu.Lock()
	if s := t.sessions[sess]; s != nil {
		s.lastSeen = time.Now()
	}
	t.mu.Unlock()
}

// EndSession records the session's duration. Unknown sessions are ignored.
func (t *Tracker) EndSession(sess string) {
	t.mu.Lock()
	s := t.sessions[sess]
	delete(t.sessions, sess)
	t.mu.Unlock()
	if s == nil {
		return
	}
	ctx := context.Background()
	var attrs []attribute.KeyValue
	if s.protocol != "" {
		attrs = append(attrs, attribute.String("mcp.protocol.version", s.protocol))
	}
	t.sessDur.Record(ctx, time.Since(s.start).Seconds(), metric.WithAttributes(attrs...))
	t.sessActive.Add(ctx, -1)
}

// Sweep ends sessions idle for longer than idle and requests that never got a
// response within it, so abandoned clients can't grow memory without bound.
func (t *Tracker) Sweep(idle time.Duration) {
	cutoff := time.Now().Add(-idle)
	var stale []string
	var orphans []*pending
	t.mu.Lock()
	for id, s := range t.sessions {
		if s.lastSeen.Before(cutoff) {
			stale = append(stale, id)
		}
	}
	for k, pd := range t.pending {
		if pd.start.Before(cutoff) {
			orphans = append(orphans, pd)
			delete(t.pending, k)
		}
	}
	t.mu.Unlock()
	for _, pd := range orphans {
		t.finish(pd, "", nil, "timeout")
	}
	for _, id := range stale {
		t.EndSession(id)
	}
}

// Close ends everything still open; call it before flushing providers.
func (t *Tracker) Close() { t.Sweep(-time.Hour) }

// LogStderr forwards one line of a stdio server's stderr as a log record.
func (t *Tracker) LogStderr(line string) {
	t.log.Info(line, "log.iostream", "stderr")
}

func (t *Tracker) capName(key, name string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.names[key] {
		return name
	}
	if len(t.names) >= t.opts.MaxNames {
		return "_other"
	}
	t.names[key] = true
	return name
}

func mcpLevel(l string) slog.Level {
	switch l {
	case "debug":
		return slog.LevelDebug
	case "info", "notice":
		return slog.LevelInfo
	case "warning":
		return slog.LevelWarn
	default: // error, critical, alert, emergency
		return slog.LevelError
	}
}

func idString(id json.RawMessage) string {
	var s string
	if json.Unmarshal(id, &s) == nil {
		return s
	}
	return string(id)
}

// hashID keeps session ids correlatable without exporting a usable value.
func hashID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:8])
}

// DefaultRedactKeys matches object keys whose values are never exported.
const DefaultRedactKeys = `(?i)pass|token|secret|key|auth|credential|cookie|session`

// secretValue matches common credential shapes inside any string, whatever
// its key: JWTs, bearer tokens, OpenAI/Anthropic-style, GitHub, Slack and AWS keys.
var secretValue = regexp.MustCompile(`eyJ[\w-]{8,}\.[\w-]{8,}\.[\w-]{8,}|(?i:bearer\s+)[\w.~+/-]{8,}=*|\bsk-[\w-]{16,}|\bgh[pousr]_[A-Za-z0-9]{30,}|\bxox[abposr]-[\w-]{10,}|\bAKIA[0-9A-Z]{16}\b`)

func (t *Tracker) capture(tool string) bool {
	return t.opts.CapturePayloads && (len(t.opts.CaptureTools) == 0 || t.opts.CaptureTools[tool])
}

// redact returns raw as JSON with secret keys and secret-looking values
// replaced, truncated to MaxPayload. Non-JSON input is treated as one string.
func (t *Tracker) redact(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return truncate(secretValue.ReplaceAllString(string(raw), "[REDACTED]"), t.opts.MaxPayload)
	}
	b, _ := json.Marshal(t.redactValue(v))
	return truncate(string(b), t.opts.MaxPayload)
}

func (t *Tracker) redactValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if t.opts.RedactKeys.MatchString(k) {
				x[k] = "[REDACTED]"
			} else {
				x[k] = t.redactValue(val)
			}
		}
	case []any:
		for i := range x {
			x[i] = t.redactValue(x[i])
		}
	case string:
		return secretValue.ReplaceAllString(x, "[REDACTED]")
	}
	return v
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
