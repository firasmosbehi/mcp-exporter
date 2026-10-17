# Changelog

All notable changes are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Fixed
- HTTP mode: gzip-compressed requests and compressed upstream responses were forwarded but not observed (#4). Request bodies are now decompressed for observation, and the upstream is asked for uncompressed responses.

## [0.1.0]

### Added
- Transparent MCP proxy for **stdio** (`mcp-exporter stdio -- <cmd>`) and **Streamable HTTP** (`mcp-exporter http -upstream <url>`), including SSE streams and sessions.
- **Traces** following the OpenTelemetry MCP semantic conventions, with trace context read from and injected into `params._meta`.
- **Metrics:** `mcp.server.operation.duration`, `mcp.client.operation.duration`, `mcp.server.session.duration`, `mcp.server.sessions.active`, `mcp.server.requests.inflight`, `mcp.message.size`, `mcp_exporter.parse.errors`. Exported over OTLP or Prometheus pull.
- **Logs:** per-request access logs with trace correlation, server `notifications/message`, and stdio server stderr.
- Opt-in redacted payload capture, hashed session ids, and TLS/mTLS for the HTTP listener.
- docker-compose reference stack (Grafana, Prometheus, Tempo, Loki), Helm chart with a sidecar, Grafana dashboard and Prometheus alert rules.
- Signed release binaries, multi-arch distroless image, SBOMs, and the Helm chart as a signed OCI artifact.
- `mcp-exporter version`, with the version also reported as the `telemetry.distro.version` resource attribute.

[Unreleased]: https://github.com/firasmosbehi/mcp-exporter/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/firasmosbehi/mcp-exporter/releases/tag/v0.1.0
