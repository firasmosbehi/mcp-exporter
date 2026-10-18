# Sending telemetry to your backend

`mcp-exporter` is configured only with the standard
[OpenTelemetry environment variables](https://opentelemetry.io/docs/languages/sdk-configuration/),
so every backend below takes **only environment variables**, with no code and no extra config files.
Set them where the exporter runs: the `env` of your Claude Desktop / Cursor entry, your shell,
the compose service, or `exporter.env` in the Helm chart.

Always set a service name, so each MCP server shows up as its own service:

```sh
export OTEL_SERVICE_NAME=github-mcp                                  # one per MCP server
export OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=prod
```

> These recipes come from each vendor's current OTLP documentation (October 2026).
> Ingest endpoints differ per region or site, so copy yours from the vendor's console.

## Try it with the demo

[deploy/compose/backends.yaml](../deploy/compose/backends.yaml) runs three things: an MCP server, the exporter in front of it,
and the OTel-instrumented demo agent. Everything exports to the backend you pick, configured only by the env file for that backend:

```sh
cp deploy/compose/backends/honeycomb.env.example deploy/compose/backends/honeycomb.env   # fill in your key
BACKEND=honeycomb docker compose -f deploy/compose/backends.yaml up --build
BACKEND=honeycomb docker compose -f deploy/compose/backends.yaml down
```

- `BACKEND` is one of `grafana-cloud`, `honeycomb`, `datadog` or `signoz`.
- Each `.env.example` holds exactly the recipe below. Filled-in `*.env` files are git-ignored.
- Add `-f deploy/compose/demo.yaml` for the full traffic mix: failures, prompts, resources and MCP Tasks.
- Within a minute you should see two services: `demo-agent`, whose `invoke_agent` traces nest the MCP calls, and `everything`, the server.

## What you'll see in any backend

- **Traces:** one span per MCP request, e.g. `tools/call get_weather`, carrying `mcp.method.name`,
  `gen_ai.tool.name`, `gen_ai.operation.name=execute_tool`, `mcp.session.id` (hashed), `error.type`, … ([full list](../README.md#telemetry)).
  Search by `gen_ai.tool.name` to find a tool, or by `error.type` to find failures.
- **Metrics:** `mcp.server.operation.duration` is the one to chart: RED per tool via `gen_ai.tool.name` and `error.type`.
- **Logs:** access logs and server log messages, correlated to traces by `trace_id`/`span_id`.

**About "LLM" or "AI" views:** several backends have dedicated views for the OpenTelemetry GenAI conventions.
Those views are built around *model calls*, keyed on attributes like `gen_ai.request.model` and token usage.
MCP spans are *tool executions* (`gen_ai.operation.name=execute_tool`) and carry no model attributes,
so expect them in the regular trace explorer rather than as LLM calls.
Where your agent framework also emits GenAI spans, MCP spans join those traces through the
`params._meta` trace context, and the tool calls appear under the agent's model calls.

---

## Grafana Cloud

On your stack's **OpenTelemetry** card (*Connections → OpenTelemetry*), generate a token. The card shows
the OTLP endpoint and a ready-made header.

```sh
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_ENDPOINT=https://otlp-gateway-prod-<region>.grafana.net/otlp
# Basic auth: base64 of "<instance id>:<token>". The raw glc_ token alone is rejected (401).
# (tr -d: GNU base64 wraps long output, which would break the header)
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic $(printf '%s' '<instance id>:<token>' | base64 | tr -d '\n')"
```

- Traces land in Tempo, metrics in Prometheus/Mimir, logs in Loki. Metric names are translated like this:
  `mcp_server_operation_duration_seconds_bucket{gen_ai_tool_name=…, error_type=…}`.
- [deploy/grafana/dashboard.json](../deploy/grafana/dashboard.json) and [alerts.yaml](../deploy/grafana/alerts.yaml)
  work unchanged: import the dashboard and pick your Prometheus data source.

## Honeycomb

```sh
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_ENDPOINT=https://api.honeycomb.io        # EU: https://api.eu1.honeycomb.io
export OTEL_EXPORTER_OTLP_HEADERS="x-honeycomb-team=<ingest api key>"
# Metrics need a dataset, otherwise they go to "unknown_metrics":
export OTEL_EXPORTER_OTLP_METRICS_HEADERS="x-honeycomb-team=<ingest api key>,x-honeycomb-dataset=mcp-metrics"
```

- With Honeycomb environments, traces and logs land in a dataset named after `OTEL_SERVICE_NAME`.
- Useful queries: `HEATMAP(duration_ms)` grouped by `gen_ai.tool.name`, and `COUNT` where `error.type exists`, grouped by `error.type`.
  For long-running MCP Tasks, `mcp.task.id` ties the creating call to its later `tasks/*` spans through span links.

## Datadog (agentless OTLP intake)

Datadog's direct OTLP intake takes **HTTP only (no gRPC)**, and **metrics must use delta temporality**.

```sh
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_ENDPOINT=https://otlp.datadoghq.com      # US1; use your site's OTLP intake host
export OTEL_EXPORTER_OTLP_HEADERS="dd-api-key=<api key>"
# Trace metrics aren't computed for directly-ingested traces unless asked for:
export OTEL_EXPORTER_OTLP_TRACES_HEADERS="dd-api-key=<api key>,compute_stats=true"
export OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta    # required by the metrics intake
```

- The exporter's Go SDK honours `OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE`. Up-down counters
  (`mcp.server.sessions.active`, `mcp.server.requests.inflight`) stay cumulative, as the spec requires.
- Already running a Datadog Agent or the DDOT Collector? Send to its OTLP receiver instead
  (`OTEL_EXPORTER_OTLP_ENDPOINT=http://<agent>:4318`) and drop the API key header.
- Datadog's Agent/LLM Observability natively ingests OTel GenAI spans (semconv 1.37+). See the note above:
  MCP tool spans have no model attributes, and they'll show in APM traces.

## SigNoz

```sh
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_ENDPOINT=https://ingest.<region>.signoz.cloud:443   # region from Settings → Ingestion
export OTEL_EXPORTER_OTLP_HEADERS="signoz-ingestion-key=<ingestion key>"       # no "Bearer"
```

- Self-hosted SigNoz: point `OTEL_EXPORTER_OTLP_ENDPOINT` at its collector (`http://<signoz-otel-collector>:4318`) and drop the header.
- `signoz-access-token` is the old name of the header. Use `signoz-ingestion-key`.

## Any other OTLP backend, or your own Collector

```sh
export OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318      # or :4317 with OTEL_EXPORTER_OTLP_PROTOCOL=grpc
```

Running a Collector in between is the usual production setup. It lets you do tail sampling (keep every trace with an
`error.type`), redact or route by attribute, and fan out to several backends without touching the exporters.

## Troubleshooting

- **Nothing arrives:** run once with `OTEL_TRACES_EXPORTER=console` to confirm spans are produced. Over stdio
  console output goes to stderr on every platform. Export failures are counted in
  `mcp_exporter.export.errors{signal}`, and a warning goes to stderr at most every 30s.
- **401/403:** check the header format. Each vendor above uses a different header name, and Grafana needs base64 of `id:token`.
- **Metrics rejected but traces fine:** for Datadog, set delta temporality. For Honeycomb, set the metrics dataset header.
