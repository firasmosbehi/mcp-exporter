# Architecture

```
            ┌──────────────────────── mcp-exporter ────────────────────────┐
client ───► │ transport (stdio pipes | HTTP reverse proxy + SSE observer)  │ ───► MCP server
            │        │ every frame (both directions)                       │
            │        ▼                                                     │
            │  jsonrpc.Parse ─► Tracker.Frame ─► spans · metrics · logs   ─┼──► OTLP / Prometheus
            └──────────────────────────────────────────────────────────────┘
```

## Frames and correlation

- **Framing:** stdio frames are newline-delimited, and lines of any length are supported. HTTP request bodies are read in
  full, up to 16 MiB. JSON responses are buffered *as they stream to the client*. SSE responses are parsed event by event as the bytes pass.
- **Parsing:** each frame is parsed as a single message or a batch. A frame that isn't valid JSON-RPC is forwarded unchanged and counted.
- **Correlation key:** a request opens a pending entry keyed by **(scope, direction, id)**, and the response travelling the other way closes it.
  - The **scope** is the id space: the stdio pipe, the HTTP session (`Mcp-Session-Id`), or, before a session exists
    (`initialize`), the single HTTP exchange.
  - **Legacy HTTP+SSE** (2024-11-05) has no session header, and responses arrive on the `GET /sse` stream, not on the POST.
    The stream's `endpoint` event names the POST URL. Its query (`sessionId`, or `session_id` in the Python SDK) becomes the
    session and scope for that stream and for POSTs to exactly that URL, and the session ends when the stream closes.
    Query strings the exporter hasn't learned this way are ignored. Gateway mode doesn't support legacy servers: their
    `endpoint` path has no route prefix, and the exporter never rewrites forwarded bytes. Give each legacy server its own exporter.
  - This scoping is what lets responses arrive on a different stream than the request: a resumed GET stream, or
    server-initiated requests answered by a later POST.
- **Other endings:** `notifications/cancelled` closes the entry as `cancelled`. Entries left open longer than `-session-idle`
  are closed as `timeout`, and idle sessions and tasks are dropped. That bounds memory when clients vanish.

## What the proxy changes, and what it doesn't

The proxy **observes and doesn't rewrite**, with these documented exceptions:

| Change | Why | Opt out |
|---|---|---|
| `traceparent` (+ `tracestate`/`baggage`) added to `params._meta` of client requests that have none | so instrumented servers continue the trace | `-inject-context=false` |
| upstream requests get `Accept-Encoding: identity` (http mode) | compressed responses can't be observed; the sidecar hop is local | |
| gzip request bodies are forwarded decompressed (http mode) | so they can be observed and injected; capped at 16 MiB | |

- **Re-encoding:** when injection re-encodes a request, ids and arguments are preserved byte for byte in meaning,
  with no HTML escaping. `params` that aren't an object (array or `null`) are never touched.
- **SSE:** responses are forwarded byte for byte. That property is fuzz-tested under arbitrary read chunking.

## stdio specifics

In stdio mode, **stdout is the protocol channel**. Two layers keep it clean:
- **All platforms:** console exporters (`OTEL_*_EXPORTER=console`) are built with an explicit stderr writer.
- **Linux and macOS:** the exporter also duplicates the real stdout for protocol frames, then points file descriptor 1 at stderr (`dup2`),
  so even a stray print from a library lands on stderr. Windows has no equivalent for already-open files.

The server's stderr is passed through and also exported as log records.

## Code map

| Package | Responsibility |
|---|---|
| `internal/jsonrpc` | parsing and encoding; `params._meta` and related-task extraction; `SetMeta` |
| `internal/observe` | OTel provider setup (`autoexport`), the error handler, and `Tracker`: correlation, spans, metrics, logs, redaction, tasks, sweeping |
| `internal/proxy` | stdio relay; HTTP reverse proxy with JSON/SSE observers, gzip handling, `/healthz`, `/readyz` |
| `cmd/mcp-exporter` | CLI, stdout reservation, pprof listener, TLS |

**Robustness:** the parser, the SSE observer, bounded gunzip and `Tracker.Frame` all have Go fuzz targets that CI runs on every PR.
Fuzzing found and fixed a crash on `"_meta": null` in v0.3.1.
