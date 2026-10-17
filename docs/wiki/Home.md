# mcp-exporter

OpenTelemetry **traces, metrics, logs and profiles for any MCP server**, with no code changes.
`mcp-exporter` is a transparent proxy. It sits between an MCP client and server, understands the JSON-RPC
traffic, and emits telemetry that follows the
[OpenTelemetry MCP semantic conventions](https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/mcp.md).

![MCP Servers dashboard](https://raw.githubusercontent.com/firasmosbehi/mcp-exporter/main/docs/images/dashboard.png)

## Start here

| I want to… | Read |
|---|---|
| see it working in one command | [[Demo]] |
| add it to Claude Desktop / Cursor / Claude Code | [[Getting Started]] |
| run it next to MCP servers in Kubernetes | [[Kubernetes]] |
| watch remote MCP servers I don't run | [[Client Mode]] |
| send data to Grafana Cloud, Honeycomb, Datadog, SigNoz | [[Backends]] |
| know every span, attribute and metric | [[Telemetry Reference]] |
| understand how the proxy works | [[Architecture]] |
| profile the exporter or my server | [[Profiles]] |
| check what data leaves the process | [[Security]] |
| fix "nothing shows up" | [[Troubleshooting]] |
| verify a release | [[Releases]] |
| contribute | [[Development]] |

## At a glance

- **Transports:** stdio (wraps the server command) and Streamable HTTP (reverse proxy, including SSE).
- **Signals:** spans per JSON-RPC request; RED metrics per method and tool; access logs plus server log messages; Go pprof and eBPF profiles.
- **MCP features:** sessions, batches, cancellation, server-initiated requests (sampling, elicitation), and **Tasks** (2025-11-25).
- **Standards only:** configured with `OTEL_*` env vars, exports OTLP (or Prometheus pull), works with any backend.
- **Overhead:** about 10 µs of CPU per request/response pair; streams are never buffered or modified.
