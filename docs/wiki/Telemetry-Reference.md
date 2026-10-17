# Telemetry Reference

Names follow the [OTel MCP semantic conventions](https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/mcp.md)
(status: Development). Names marked † are this project's, for things the conventions don't cover yet.

## Spans

One span per JSON-RPC **request**, from the request until its response, cancellation or timeout.
- **Name:** `{mcp.method.name} {target}`, where the target is the tool or prompt name, e.g. `tools/call get_weather` or `prompts/get summarize`.
- **Kind:** `SERVER` for requests the observed side receives, `CLIENT` for those it sends (see [[Client Mode]]).

| Attribute | When |
|---|---|
| `mcp.method.name` | always |
| `jsonrpc.request.id` | always (string form) |
| `network.transport` | `pipe` (stdio) or `tcp` (http) |
| `mcp.session.id` | in a session; **truncated SHA-256**, never the raw id |
| `mcp.protocol.version`, `mcp.server.name` †, `mcp.client.name` † | after `initialize` |
| `gen_ai.tool.name`, `gen_ai.operation.name=execute_tool` | `tools/call` |
| `gen_ai.prompt.name` | `prompts/get` |
| `mcp.resource.uri` | `resources/read`, `/subscribe`, `/unsubscribe` |
| `server.address`, `server.port` | `CLIENT` spans in http mode |
| `gen_ai.tool.call.arguments`, `gen_ai.tool.call.result` | opt-in, `-capture-payloads`; redacted and truncated |
| `rpc.response.status_code` | JSON-RPC error responses |
| `error.type` | failures (see below) |
| `mcp.task.id` †, `mcp.task.status` † | MCP Tasks (see below) |

**Parenting:** trace context in `params._meta` (`traceparent`, `tracestate`, `baggage`) becomes the parent. An HTTP
`traceparent` header becomes a **span link** if `_meta` also has context, and the parent otherwise.

### error.type

| Value | Meaning |
|---|---|
| a JSON-RPC code, e.g. `-32603` | error response; span status ERROR with the error message |
| `tool_error` | `tools/call` result with `isError: true` |
| `cancelled` | `notifications/cancelled` for the request |
| `timeout` | no response within `-session-idle` |
| `duplicate_id` | the id was reused before a response arrived |

On `SERVER` spans, the caller-mistake codes **-32700, -32600, -32601, -32602, -32002** set `rpc.response.status_code`
but are *not* errors (no `error.type`, status unset), per the conventions. On `CLIENT` spans every code is an error.

### MCP Tasks (2025-11-25)

- **Creation:** a task-augmented request (`params.task`) whose response is a `CreateTaskResult` gets `mcp.task.id` and `mcp.task.status`.
- **Follow-ups:** `tasks/get`, `tasks/result`, `tasks/cancel`, and any message with `_meta["io.modelcontextprotocol/related-task"]`
  get `mcp.task.id` and a **span link to the creating span**. Polled statuses are recorded as `mcp.task.status`.

## Metrics

| Metric | Type | Unit | Attributes |
|---|---|---|---|
| `mcp.server.operation.duration` | histogram | s | `mcp.method.name`, `gen_ai.tool.name`, `gen_ai.prompt.name`, `error.type`, `rpc.response.status_code`, `mcp.protocol.version`, `network.transport` |
| `mcp.client.operation.duration` | histogram | s | same, for requests the observed side sends |
| `mcp.{server\|client}.session.duration` | histogram | s | `mcp.protocol.version` |
| `mcp.{server\|client}.sessions.active` † | up-down counter | {session} | |
| `mcp.{server\|client}.requests.inflight` † | up-down counter | {request} | method, tool, prompt |
| `mcp.task.duration` † | histogram | s | origin method/tool/prompt, `mcp.task.status` |
| `mcp.message.size` † | histogram | By | `mcp.direction` (`client_to_server`/`server_to_client`), transport |
| `mcp_exporter.parse.errors` † | counter | {frame} | frames that weren't JSON-RPC (forwarded untouched) |
| `mcp_exporter.export.errors` † | counter | {error} | `signal`: SDK errors, mostly failed exports |
| `otel.sdk.*` | various | | the Go SDK's own metrics: processed/dropped spans and logs, exporter results, queues |

- **Duration buckets** (s): 0.01 … 300. Tasks: 0.1 … 3600.
- **Cardinality guards:** session ids, request ids and task ids never become metric attributes. Tool and prompt names are capped
  at 500 distinct values, with the rest folded into `_other`.
- **Prometheus names** (after OTLP translation) look like `mcp_server_operation_duration_seconds_bucket{gen_ai_tool_name=…}`.

## Logs

| Record | Fields |
|---|---|
| `mcp request` (INFO), one per finished request | `mcp.method.name`, `duration_ms`, `error.type` (when failed); `trace_id`/`span_id` |
| `mcp server log`, one per server `notifications/message` | severity from the MCP level (`debug`→DEBUG, `info`/`notice`→INFO, `warning`→WARN, else ERROR), `mcp.log.level`, `mcp.log.logger`, `mcp.log.data` (redacted) |
| `mcp task` (INFO), when a task ends | `mcp.task.id`, `mcp.task.status`, `duration_ms`; trace context of the creating span |
| stderr lines (stdio mode) | `log.iostream=stderr` |

## Resource

`service.name` (from `OTEL_SERVICE_NAME` or `-service-name`), `host.name`, the `telemetry.sdk.*` attributes,
`telemetry.distro.name=mcp-exporter`, `telemetry.distro.version`, and anything in `OTEL_RESOURCE_ATTRIBUTES`.
