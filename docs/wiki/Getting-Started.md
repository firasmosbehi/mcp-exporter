# Getting Started

## Install

```sh
# Binary: linux/darwin/windows × amd64/arm64 from https://github.com/firasmosbehi/mcp-exporter/releases, or:
go install github.com/firasmosbehi/mcp-exporter/cmd/mcp-exporter@latest
# Container image (multi-arch, distroless, non-root)
docker pull ghcr.io/firasmosbehi/mcp-exporter:latest
```

## Pick a mode

| Your MCP server is… | Use | The exporter… |
|---|---|---|
| a local command (most desktop setups) | `mcp-exporter stdio -- <command> [args]` | spawns the command and relays stdin/stdout |
| a Streamable HTTP endpoint you run | `mcp-exporter http -upstream http://server:3001` | listens on `:8080` and proxies, request paths unchanged |
| a remote endpoint you don't run | add `-role client` (see [[Client Mode]]) | observes from the agent's side |

## Desktop clients

Prefix the server's command with `mcp-exporter stdio --`, and point the exporter at a Collector or backend.

**Claude Desktop** (`claude_desktop_config.json`):
```jsonc
"mcpServers": {
  "github": {
    "command": "mcp-exporter",
    "args": ["stdio", "--", "npx", "-y", "@modelcontextprotocol/server-github"],
    "env": {
      "OTEL_SERVICE_NAME": "github-mcp",
      "OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
      "GITHUB_PERSONAL_ACCESS_TOKEN": "…"
    }
  }
}
```
The server inherits the exporter's environment, so its own variables (like the token above) go in the same `env` block.

**Claude Code:**
```sh
claude mcp add github -e OTEL_SERVICE_NAME=github-mcp -- mcp-exporter stdio -- npx -y @modelcontextprotocol/server-github
```

**Cursor** (`.cursor/mcp.json`) takes the same `command` / `args` / `env` shape as Claude Desktop.

No backend yet? Run the [[Demo]] stack and use `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`.
To look at raw telemetry, set `OTEL_TRACES_EXPORTER=console` (it goes to stderr, so stdout stays the protocol channel).

## A server you run over HTTP

```sh
mcp-exporter http -listen :8080 -upstream http://127.0.0.1:3001 \
  -service-name weather-mcp
# clients now connect to http://<host>:8080/mcp instead of :3001/mcp
```
`/healthz` (liveness) and `/readyz` (503 while the upstream is unreachable) are served on the same port.

Next: [[Configuration]], [[Backends]], [[Kubernetes]].
