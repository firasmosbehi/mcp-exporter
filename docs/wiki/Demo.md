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
| `demo-agent` | a shell-script "agent" generating realistic traffic |

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

![A tasks/result span](https://raw.githubusercontent.com/firasmosbehi/mcp-exporter/main/docs/images/trace.png)

The demo agent sends no trace context, so every request is its own trace. That's what you get from any uninstrumented client.
When the agent is instrumented, MCP spans nest under its spans through `params._meta` (see [[Architecture]]).

**Your own traffic:** point any MCP client at `http://localhost:8080/mcp`, for example the
[MCP Inspector](https://github.com/modelcontextprotocol/inspector).
