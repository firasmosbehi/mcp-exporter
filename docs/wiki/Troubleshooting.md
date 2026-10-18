# Troubleshooting

## Nothing shows up in my backend

1. **Check that spans are produced:** run once with `OTEL_TRACES_EXPORTER=console`. In stdio mode they print to **stderr**
   (on every platform).
2. **Check exports:** `mcp_exporter.export.errors{signal}` counts failures, and a warning is written to stderr at most every 30 s.
   With `OTEL_METRICS_EXPORTER=prometheus` you can see this counter even while OTLP is down.
3. **Endpoint form:** `OTEL_EXPORTER_OTLP_ENDPOINT` is a *base* URL (`http://collector:4318`), and the SDK appends `/v1/traces`.
   The per-signal `…_TRACES_ENDPOINT` variables take the *full* URL.
4. **Protocol:** the default is `http/protobuf` on `:4318`. For `:4317`, set `OTEL_EXPORTER_OTLP_PROTOCOL=grpc`.
5. **Auth:** the header format differs per vendor (see [[Backends]]). Grafana Cloud needs base64 of `instanceID:token`.
6. **Metrics later than traces:** metrics export every 60 s by default (`OTEL_METRIC_EXPORT_INTERVAL`).

## My client broke after adding the exporter (stdio)

- **Garbage on stdout:** something in the *server* is writing non-JSON-RPC to stdout. The exporter forwards it unchanged
  and counts it in `mcp_exporter.parse.errors`.
- **Old versions:** on v0.3.0 and earlier, a request with `"_meta": null` crashed the exporter. Upgrade to v0.3.1 or later.
- **Server needs its own env vars:** in a desktop client config, put them in the same `env` block as the `OTEL_*` variables.
- **`OTEL_*` ignored when an SDK agent launches the exporter:** both official SDKs' stdio clients start the server with only a
  small allowlist of variables (`HOME`, `LOGNAME`, `PATH`, `SHELL`, `TERM`, `USER` on Unix), unless you pass `env`. The exporter then falls
  back to `localhost:4318` and logs `connection refused`. Pass the variables explicitly. In Python:
  `StdioServerParameters(command="mcp-exporter", args=[...], env={**get_default_environment(), "OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318"})`.
  In TypeScript: `new StdioClientTransport({command, args, env: {...getDefaultEnvironment(), OTEL_EXPORTER_OTLP_ENDPOINT: "…"}})`.

## HTTP mode

- **`/readyz` returns 503:** the upstream refuses TCP connections. Check `-upstream` and the server's port.
- **Responses aren't compressed any more:** expected. The exporter asks the upstream for `identity` encoding so it can observe responses.
- **OAuth token rejected in client mode:** the server enforces token audience. Use the `mcp-remote` form ([[Client Mode]]).
- **Sessions break with several replicas:** you need session affinity, as without the exporter ([[Kubernetes]]).

## Spans look wrong

- **Every request is its own trace:** the client sends no trace context. That's normal for uninstrumented agents.
- **`-32601` isn't counted as an error:** by design on the server side (a caller mistake). See [[Telemetry Reference]].
- **`_other` in the tool name:** more than 500 distinct tool names. This cardinality guard protects your metrics backend.
- **A task's duration is missing:** it's recorded when a terminal status is *observed*. If the client never polls again, the
  task is dropped after `-session-idle` without an outcome.

## FAQ

- **Does it support stdio and HTTP at the same time?** Run one exporter per server; each is a single process.
- **Overhead?** About 10 µs of CPU per request/response pair (`go test ./internal/observe -bench .`). Streams aren't buffered.
- **Can it block or modify tool calls?** No. It's observability only. Policy and gateways are out of scope.
