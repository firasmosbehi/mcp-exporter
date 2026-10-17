# mcp-exporter

[![ci](https://github.com/firasmosbehi/mcp-exporter/actions/workflows/ci.yml/badge.svg)](https://github.com/firasmosbehi/mcp-exporter/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/firasmosbehi/mcp-exporter.svg)](https://pkg.go.dev/github.com/firasmosbehi/mcp-exporter)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

OpenTelemetry **traces, metrics and logs for any MCP server**, with no code changes.
`mcp-exporter` is a transparent proxy that sits between an MCP client and server,
understands the JSON-RPC traffic, and emits telemetry that follows the
[OpenTelemetry MCP semantic conventions](https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/mcp.md).
Any OTLP backend works (Grafana, Datadog, Honeycomb, SigNoz, Jaeger, ...), and so does Prometheus scraping.

```
client ──► mcp-exporter ──► MCP server
              ├─ OTLP ──► OTel Collector / backend
              └─ /metrics (Prometheus pull, optional)
```

| Transport | Command |
|---|---|
| stdio (local servers) | `mcp-exporter stdio -- <server command> [args...]` |
| Streamable HTTP | `mcp-exporter http -listen :8080 -upstream http://server:3001` |

## Quick start

**Laptop (Claude Desktop, Cursor, Claude Code):** prefix the server command.

```jsonc
// claude_desktop_config.json
"mcpServers": {
  "github": {
    "command": "mcp-exporter",
    "args": ["stdio", "--", "npx", "-y", "@modelcontextprotocol/server-github"],
    "env": { "OTEL_SERVICE_NAME": "github-mcp", "OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318" }
  }
}
```
```sh
claude mcp add github -- mcp-exporter stdio -- npx -y @modelcontextprotocol/server-github
```

**Docker:** a full stack with an example server, the exporter, and Grafana + Prometheus + Tempo + Loki, plus a ready-made dashboard:

```sh
docker compose -f deploy/compose/docker-compose.yaml up --build
# MCP endpoint http://localhost:8080/mcp · Grafana http://localhost:3000 (dashboard "MCP Servers")
```

**Kubernetes:** the Helm chart runs your server with the exporter as a sidecar:

```sh
helm install weather deploy/helm/mcp-exporter \
  --set server.image=ghcr.io/acme/weather-mcp:1.2.0 --set server.port=3000 \
  --set exporter.env.OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector.observability:4318
# Prometheus Operator instead of OTLP metrics:
#   --set exporter.prometheus.enabled=true --set serviceMonitor.enabled=true
```

## Configuration

Export is configured only through the standard
[OTel environment variables](https://opentelemetry.io/docs/languages/sdk-configuration/):

| Variable | Common values |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://collector:4318` (`OTEL_EXPORTER_OTLP_PROTOCOL=grpc` for `:4317`) |
| `OTEL_TRACES_EXPORTER` / `OTEL_LOGS_EXPORTER` | `otlp` (default), `console`, `none` |
| `OTEL_METRICS_EXPORTER` | `otlp` (default), `prometheus` (serves `:9464/metrics`), `console`, `none` |
| `OTEL_TRACES_SAMPLER` (+ `_ARG`) | `parentbased_traceidratio` + `0.1` |
| `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES` | identify the server (`deployment.environment.name=prod,...`) |
| `OTEL_EXPORTER_OTLP_HEADERS`, `_CERTIFICATE`, `_CLIENT_CERTIFICATE`, `_CLIENT_KEY` | backend auth and mTLS |

Flags (`mcp-exporter <mode> -h`):

| Flag | Default | |
|---|---|---|
| `-service-name` | `mcp-server` | used when `OTEL_SERVICE_NAME` is unset |
| `-inject-context` | `true` | adds `traceparent` to `params._meta` of client requests that lack it, so instrumented servers join the trace |
| `-capture-payloads` | `false` | records tool arguments and results on spans, redacted and truncated |
| `-max-payload` | `4096` | truncation limit for captured payloads |
| `-listen`, `-upstream` | `:8080`, — | http mode; request paths pass through to the upstream base URL |
| `-session-idle` | `30m` | http mode; ends abandoned sessions and requests that never got a response |
| `-tls-cert`, `-tls-key`, `-tls-client-ca` | — | http mode; TLS, and mTLS when a client CA is given |

## Telemetry

**Spans:** one per JSON-RPC request, named `{mcp.method.name} {tool|prompt}` (e.g. `tools/call get_weather`).
Requests from the client are `SERVER` spans. Server-initiated requests (sampling, elicitation, roots) are `CLIENT` spans.
Attributes: `mcp.method.name`, `jsonrpc.request.id`, `gen_ai.tool.name`, `gen_ai.operation.name`,
`gen_ai.prompt.name`, `mcp.resource.uri`, `mcp.session.id` (hashed), `mcp.protocol.version`,
`mcp.server.name`, `mcp.client.name`, `network.transport`, `rpc.response.status_code`, `error.type`.
Parent context comes from `params._meta` (`traceparent`/`tracestate`/`baggage`), and an HTTP `traceparent` header becomes a span link.

**Errors:** `error.type` is the JSON-RPC error code, `tool_error` for results with `isError: true`,
`cancelled` (`notifications/cancelled`), or `timeout` (no response within `-session-idle`).
Per the conventions, the caller-side codes `-32700 -32600 -32601 -32602 -32002` are recorded
in `rpc.response.status_code` but are not counted as server errors.

**Metrics:**

| Metric | Type | Key attributes |
|---|---|---|
| `mcp.server.operation.duration` (s) | histogram | method, tool, prompt, `error.type`, `rpc.response.status_code`, protocol version |
| `mcp.client.operation.duration` (s) | histogram | server-initiated requests |
| `mcp.server.session.duration` (s) | histogram | protocol version |
| `mcp.server.sessions.active` | up-down counter | |
| `mcp.server.requests.inflight` | up-down counter | method, tool |
| `mcp.message.size` (By) | histogram | `mcp.direction`, transport |
| `mcp_exporter.parse.errors` | counter | frames that were not JSON-RPC, forwarded untouched |

Session and request ids never become metric labels. Tool and prompt names are capped at 500 distinct values, and the rest fold into `_other`.

**Logs:** one access record per request (carrying `trace_id`/`span_id`), server `notifications/message`
with MCP levels mapped to OTel severities, and the server's stderr in stdio mode.

**Dashboards and alerts:** see [deploy/grafana/dashboard.json](deploy/grafana/dashboard.json) and
[deploy/grafana/alerts.yaml](deploy/grafana/alerts.yaml) (error rate, failing tools, slow tools, parse errors).

## Security

- Payload capture is off by default. When enabled, keys matching `pass|token|secret|key|auth|credential|cookie|session` are redacted.
- `Authorization`, cookies and other headers are never recorded. `Mcp-Session-Id` is exported only as a truncated SHA-256.
- In stdio mode, stdout is reserved at the file-descriptor level for protocol frames, so a console exporter or a stray print can't corrupt the stream.
- Release images are distroless, run as a non-root user, and are cosign-signed, with SBOMs ([.goreleaser.yaml](.goreleaser.yaml)).

## Overhead

About 8 µs of CPU per request/response pair, including trace-context injection (`go test ./internal/observe -bench .`).
SSE streams are observed as they pass through and are never buffered or modified.

## Roadmap

- **Profiles:** OTLP Profiles are in public Alpha (Mar 2026). Server-side profiling is best done today with the
  [OTel eBPF profiler](https://github.com/open-telemetry/opentelemetry-ebpf-profiler), and `grafana/otel-lgtm` already ships Pyroscope.
- Client-side mode (`mcp.client.*` from the agent's point of view).
- Upstream readiness checks for `/readyz`.

## Contributing

Issues and PRs are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md), the [milestones](https://github.com/firasmosbehi/mcp-exporter/milestones),
and the [code of conduct](CODE_OF_CONDUCT.md). Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## License

[Apache License 2.0](LICENSE)
