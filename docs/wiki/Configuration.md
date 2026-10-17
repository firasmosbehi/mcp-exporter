# Configuration

Three layers, with no config file:
1. **Mode and flags**: `mcp-exporter stdio [flags] -- <cmd>` or `mcp-exporter http [flags]`.
2. **Standard OpenTelemetry environment variables**: everything about *where* telemetry goes.
3. `mcp-exporter version` prints the version, which is also reported as the `telemetry.distro.version` resource attribute.

## Flags

| Flag | Modes | Default | Meaning |
|---|---|---|---|
| `-service-name` | both | `mcp-server` | `service.name` when `OTEL_SERVICE_NAME` is unset |
| `-role` | both | `server` | `client` to observe from the agent's side ([[Client Mode]]) |
| `-inject-context` | both | `true` | add W3C `traceparent`/`tracestate`/`baggage` to `params._meta` of client requests that lack it |
| `-capture-payloads` | both | `false` | record tool arguments/results on spans (`gen_ai.tool.call.arguments`/`.result`), redacted |
| `-capture-tools` | both | all | comma-separated allowlist of tools to capture payloads for |
| `-redact-keys` | both | `(?i)pass\|token\|secret\|key\|auth\|credential\|cookie\|session` | regexp of object keys whose values are redacted (replaces the default) |
| `-max-payload` | both | `4096` | truncate captured payloads and server log data to this many bytes |
| `-pprof` | both | off | serve Go pprof on this address, e.g. `127.0.0.1:6060` ([[Profiles]]) |
| `-listen` | http | `:8080` | proxy listen address (`/healthz`, `/readyz` are served here too) |
| `-upstream` | http | required | base URL of the MCP server; request paths pass through unchanged |
| `-session-idle` | http | `30m` | end sessions, requests and tasks idle this long |
| `-tls-cert`, `-tls-key` | http | | serve TLS |
| `-tls-client-ca` | http | | require client certificates signed by this CA (mTLS); needs `-tls-cert`/`-tls-key` |

## Environment variables

All read by the OpenTelemetry SDK. The most useful:

| Variable | Values |
|---|---|
| `OTEL_SERVICE_NAME` | the MCP server's name: one per server |
| `OTEL_RESOURCE_ATTRIBUTES` | `deployment.environment.name=prod,team=payments` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://collector:4318`; per signal: `OTEL_EXPORTER_OTLP_{TRACES,METRICS,LOGS}_ENDPOINT` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `http/protobuf` (default), `grpc` |
| `OTEL_EXPORTER_OTLP_HEADERS` | `key=value,key2=value2` (auth for your backend) |
| `OTEL_EXPORTER_OTLP_CERTIFICATE`, `_CLIENT_CERTIFICATE`, `_CLIENT_KEY` | TLS/mTLS to the backend |
| `OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE` | `cumulative` (default), `delta` (needed by Datadog's intake), `lowmemory` |
| `OTEL_TRACES_EXPORTER`, `OTEL_LOGS_EXPORTER` | `otlp` (default), `console`, `none` |
| `OTEL_METRICS_EXPORTER` | `otlp` (default), `prometheus`, `console`, `none` |
| `OTEL_EXPORTER_PROMETHEUS_HOST` / `_PORT` | default `localhost` / `9464`; use `0.0.0.0` in containers |
| `OTEL_TRACES_SAMPLER` / `_ARG` | `parentbased_traceidratio` / `0.1` |
| `OTEL_METRIC_EXPORT_INTERVAL` | milliseconds, default 60000 |
| `OTEL_GO_X_OBSERVABILITY` | SDK self-metrics (`otel.sdk.*`), **on by default** here; `false` turns them off |

In stdio mode the MCP server inherits the exporter's environment, so the `OTEL_*` variables reach it too.
That's harmless unless the server is itself instrumented, and then it's useful.
