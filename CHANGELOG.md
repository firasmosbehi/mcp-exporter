# Changelog

All notable changes are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.3.1]

### Fixed
- **Crash:** a request with `"params": {"_meta": null}` (or `"params": null`) panicked while injecting trace context. That killed the process in stdio mode, and failed the request in HTTP mode. It affected every release up to 0.3.0, and the workaround is `-inject-context=false`. Found by fuzzing (#38).
- Injecting trace context no longer HTML-escapes `&`, `<` and `>` in request ids and arguments (#38).

### Added
- Fuzz targets for the JSON-RPC parser, the SSE observer, bounded gunzip and `Tracker.Frame`, run briefly in CI on every PR (#38).

## [0.3.0]

### Added
- [docs/backends.md](docs/backends.md): env-var recipes for Grafana Cloud, Honeycomb, Datadog (agentless, delta metrics) and SigNoz, plus what to expect in GenAI "LLM views" and troubleshooting (#14).
- Client mode, `-role client` (#13): observes from the agent's side. Agent-sent requests become CLIENT spans with `server.address`/`server.port` and `mcp.client.operation.duration`, where every error code counts. Session metrics are named `mcp.client.*`. Works over stdio (e.g. wrapping `mcp-remote`) and HTTP.
- `-pprof <addr>` serves the exporter's Go pprof on a separate listener, and Helm gets `exporter.pprof.enabled` (#11).
- Profiles overlay `deploy/compose/profiling.yaml`: a Collector pprof receiver scraping the exporter, plus the OpenTelemetry eBPF profiler profiling the MCP server, both into Pyroscope. There's also a Kubernetes DaemonSet in `deploy/kubernetes/ebpf-profiler.yaml`, and CI's e2e test now checks that profiles arrive (#11, #12).

### Changed
- The reference stack moves from `grafana/otel-lgtm` 0.11.10 to 0.35.0, whose Collector speaks the current OTLP Profiles protocol.

## [0.2.0]

### Added
- MCP Tasks support (#6): `mcp.task.id`/`mcp.task.status` on creating, polling and related spans, span links back to the creating request, and a `mcp.task.duration` histogram (creation to terminal status).
- Self-observability (#9): `mcp_exporter.export.errors{signal}` counts SDK and export failures, the SDK's own `otel.sdk.*` metrics are enabled by default (`OTEL_GO_X_OBSERVABILITY=false` to opt out), SDK error logging to stderr is rate-limited to one line per 30s, and there's a new `MCPExporterExportFailing` alert.
- `-redact-keys` to replace the redacted-key pattern, and `-capture-tools` to capture payloads only for listed tools (#8).
- Secret-shaped values (JWTs, bearer tokens, `sk-`/GitHub/Slack tokens, AWS access key ids) are redacted wherever they appear in captured payloads (#8).

### Changed
- Server `notifications/message` log data is now redacted like captured payloads, not just truncated (#8).
- HTTP mode: `/readyz` now returns 503 while the upstream is unreachable (TCP dial, 1s timeout, result cached 2s), so Kubernetes takes the pod out of the Service when the MCP server dies (#5). `/healthz` is unchanged.

### Fixed
- Windows: stdio mode with `OTEL_*_EXPORTER=console` wrote telemetry into the protocol stream. It now exits with a clear error, because Windows has no fd-level stdout swap (#10).
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

[Unreleased]: https://github.com/firasmosbehi/mcp-exporter/compare/v0.3.1...HEAD
[0.3.1]: https://github.com/firasmosbehi/mcp-exporter/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/firasmosbehi/mcp-exporter/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/firasmosbehi/mcp-exporter/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/firasmosbehi/mcp-exporter/releases/tag/v0.1.0
