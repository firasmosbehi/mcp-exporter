# Draft: proposals to the OpenTelemetry MCP semantic conventions

**Status: draft, not filed.** This is text for issues in
[open-telemetry/semantic-conventions-genai](https://github.com/open-telemetry/semantic-conventions-genai).
`mcp-exporter` emits these names today because the MCP conventions don't define them
(they're listed in `projectDefined` in `internal/observe/semconv_test.go`). When upstream adopts a name,
CI's `TestEmittedNamesAreInSemconv` flags it, and we migrate (see CONTRIBUTING.md, "Semantic convention changes").

## 1. Tasks (MCP 2025-11-25)

MCP Tasks let a request run as a durable task: a `CreateTaskResult` now, then polling via
`tasks/get` / `tasks/result` / `tasks/cancel`, plus `notifications/tasks/status`. Today a task's lifecycle is spread
across unrelated spans.

| Proposed | Type | On | Notes |
|---|---|---|---|
| `mcp.task.id` | string | spans of the creating request, `tasks/*`, and messages carrying `_meta["io.modelcontextprotocol/related-task"]` | **never** a metric attribute (unbounded) |
| `mcp.task.status` | enum: `working`, `input_required`, `completed`, `failed`, `cancelled` | spans where a status is observed; task metric | |
| `mcp.task.duration` | histogram, s | creation → first terminal status observed | attributes: `mcp.method.name` and `gen_ai.tool.name` of the creating request, `mcp.task.status` |

We also suggest a recommendation that `tasks/*` spans carry a **span link** to the creating request's span, since they're usually
separate traces.

## 2. Peer identity from `initialize`

| Proposed | From | Why |
|---|---|---|
| `mcp.server.name` (+ `mcp.server.version`) | `InitializeResult.serverInfo` | identify the server implementation independent of `service.name`, e.g. in client-side telemetry |
| `mcp.client.name` (+ `mcp.client.version`) | `InitializeRequest.params.clientInfo` | which agent or host is calling: useful on every server span |

## 3. Transport-level metrics

| Proposed | Type | Notes |
|---|---|---|
| `mcp.{client,server}.session.active`, or an up-down counter of open sessions | up-down counter | complements `mcp.*.session.duration`, which only records ended sessions |
| `mcp.message.size` | histogram, By | per JSON-RPC frame, attribute `mcp.direction` (or reuse `rpc.message.*` conventions if preferred) |
