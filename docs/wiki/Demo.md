# Demo

```sh
git clone https://github.com/firasmosbehi/mcp-exporter && cd mcp-exporter
deploy/compose/demo.sh            # then open http://localhost:3000/d/mcp-exporter
deploy/compose/demo.sh down       # stop and clean up
PROFILES=1 deploy/compose/demo.sh # also CPU profiles (Linux Docker, privileged)
```

What starts:

| Service | Role |
|---|---|
| `mcp-server` | the reference [server-everything](https://github.com/modelcontextprotocol/servers/tree/main/src/everything), Streamable HTTP on :3001 |
| `mcp-exporter` | the proxy, on http://localhost:8080/mcp |
| `lgtm` | `grafana/otel-lgtm`: OTel Collector, Prometheus, Tempo, Loki, Pyroscope and Grafana (:3000, no login), with the MCP dashboard provisioned |
| `demo-agent` | a shell-script "agent" generating realistic traffic, with no trace context |
| `traced-agent` | an OTel-instrumented Go agent ([cmd/demo-agent](https://github.com/firasmosbehi/mcp-exporter/blob/main/cmd/demo-agent/main.go)): MCP spans nest under its steps |

The demo agent:
- opens a session and keeps a **GET stream** open, as real clients do, so server log messages flow;
- turns on the server's simulated logging;
- loops over a weighted mix: `echo`, `get-sum`, `get-structured-content`, `get-tiny-image`,
  a **2–5 s** `trigger-long-running-operation`, a call to a **nonexistent tool**, a call with **invalid arguments**,
  `prompts/get`, `resources/read` and `tools/list`;
- runs an **MCP Task**: `simulate-research-query` (task-required) → poll `tasks/get` until terminal → `tasks/result`;
- **rotates the session** every ~40 rounds (`DELETE` + new `initialize`), so session duration metrics move.

## Where to look

- **Dashboard** (Dashboards → MCP Servers), in four rows:
  - **Server:** request rate, error % (~7%, from the deliberate failures), tool latency (the long-running operation stands out), sessions and message sizes.
  - **MCP Tasks:** task duration and status.
  - **Client mode:** empty here, filled by `-role client` exporters.
  - **Exporter health:** failed exports, unparsed frames, dropped telemetry and SDK queue fill.
- **Traces** (Explore → Tempo): `{span.gen_ai.tool.name != ""}`, `{span.error.type != ""}`, or `{span.mcp.task.id != ""}`
  to follow a task. Task spans link back to the call that created the task.
- **Logs** (Explore → Loki): `{service_name="everything"}`. Access logs have `mcp.method.name`, `duration_ms` and `error.type`;
  server logs have `mcp.log.level` and `mcp.log.data`. Both carry `trace_id`.
- **Metrics** (Explore → Prometheus): `mcp_server_operation_duration_seconds`, `mcp_task_duration_seconds`, …

- **Agent traces** (Explore → Tempo): `{resource.service.name="demo-agent"}`. Each step (`invoke_agent demo-agent`) holds
  two `chat demo-model` spans (`gen_ai.request.model`, token usage) and the MCP `tools/call` spans the exporter recorded. These
  nest because the agent puts its trace context into `params._meta` (see [[Architecture]]). Every few steps one call runs as
  an MCP Task, and its `tasks/get` polls link back to the call that created it.

![An agent step with its model calls and MCP tool calls](https://raw.githubusercontent.com/firasmosbehi/mcp-exporter/main/docs/images/agent-trace.png)

The shell `demo-agent` sends no trace context, so each of its requests is its own trace. That's what you get from any uninstrumented client.

**Your own traffic:** point any MCP client at `http://localhost:8080/mcp`, for example the
[MCP Inspector](https://github.com/modelcontextprotocol/inspector).
