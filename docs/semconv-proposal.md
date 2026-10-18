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

## 4. Protocol 2026-07-28 (stateless MCP)

The conventions predate the stateless revision. These are the points we had to decide on ourselves:

- **Source of `mcp.protocol.version`:** 2026-07-28 has no `initialize`. We read the version from each request's
  `_meta["io.modelcontextprotocol/protocolVersion"]`. Proposed wording: "the version negotiated at initialization or, for
  stateless protocol versions, the one the request declares."
- **Peer identity (section 2):** the same holds for `_meta["io.modelcontextprotocol/clientInfo"]` on requests and
  `_meta["io.modelcontextprotocol/serverInfo"]` on results.
- **`mcp.session.id`:** it no longer applies from 2026-07-28. Its "when part of a session" condition already covers that, but a note would help.
- **`subscriptions/listen`:** a request whose response is a long-lived stream. We propose that ending it, whether the client
  closes it, cancels it or the server ends it gracefully, is **not** an error, and that it's excluded from in-flight counts.
- **New caller-error codes on server spans:** `-32020` HeaderMismatch, `-32021` MissingRequiredClientCapability and `-32022`
  UnsupportedProtocolVersion are caller mistakes, like `-32602`. We propose adding them to the codes that are not errors on
  `SERVER` spans. Until then we follow the conventions as written and count them as errors.
- **Tasks extension:** `CreateTaskResult` becomes flat (`resultType: "task"`), no request opt-in is needed, and
  `notifications/tasks` replaces `notifications/tasks/status`. Section 1 applies unchanged.
