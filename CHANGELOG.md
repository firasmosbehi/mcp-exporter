# Changelog

All notable changes are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- `deploy/compose/backends.yaml` runs the demo (MCP server, exporter, instrumented agent) against Grafana Cloud, Honeycomb,
  Datadog or SigNoz. Choose one with `BACKEND=<name>`; each backend has an env template in `deploy/compose/backends/` (#14).

### Fixed
- Wiki sync now compares files by content. Same-size edits, such as every version bump, could be skipped as unchanged, which left the wiki's Releases page at 0.6.1.

## [0.7.0] - 2026-10-18

### Added
- **MCP protocol 2026-07-28 (stateless).**
  - `mcp.protocol.version`, `mcp.client.name` and `mcp.server.name` now come from each message's `_meta` (`io.modelcontextprotocol/protocolVersion`, `/clientInfo`, `/serverInfo`). Before this, they were missing whenever there was no `initialize`.
  - `subscriptions/listen` spans last as long as the subscription and end without an error. Before, they ended as `timeout` errors. They are never idle-swept or counted as in-flight.
  - The Tasks extension: flat `resultType: "task"` results, with or without opt-in, plus `notifications/tasks` and `tasks/update`.
  - Tested against the Python SDK 2.3 `MCPServer` over HTTP and stdio. The CI smoke test runs one (`deploy/compose/stateless.yaml`).
- Wiki:
  - Architecture has a **Tested with** table: client and server SDK pairs and their transports, including the Python SDK on all three.
  - Troubleshooting explains that SDK stdio clients don't pass `OTEL_*` to the exporter unless `env` is given.

### Fixed
- HTTP, no session: a request whose response ends without an answer now ends right away. That's `error.type=cancelled` when the stream closed, or the HTTP status when the upstream failed (`502` if unreachable). Before, it ended as a `timeout` after `-session-idle`.

## [0.6.1] - 2026-10-18

### Fixed
- **Legacy HTTP+SSE servers** (protocol 2024-11-05: `GET /sse` + `POST /message?sessionId=`) are now observed correctly.
  - Responses arrive on the GET stream, so they never matched their POSTs. Every request ended as a false `timeout` error after `-session-idle`, and no session was tracked.
  - The session now comes from the stream's `endpoint` event and ends when the stream closes.
  - This is not supported in gateway mode.

## [0.6.0] - 2026-10-18

### Added
- **Gateway mode** (#54): `-route NAME=URL` (repeatable) puts one exporter in front of several MCP servers.
  - `/NAME/...` is proxied with the prefix stripped, and each route exports its telemetry as `service.name=NAME`.
  - Sessions and request ids are tracked per route.
  - `/readyz` aggregates all upstreams, and `/NAME/readyz` checks one.
  - Helm: `gateway.routes` deploys the exporter without a server sidecar.
  - Compose: the `gateway.yaml` overlay, covered by the smoke test.

- Wiki, Client Mode: why OAuth servers that enforce token audience can't work through the HTTP client form, with the RFC 9728 and RFC 8707 details.
  Recipes: the `mcp-remote` stdio form, or `validateResourceURL` in agents built on the TypeScript SDK (#55).

- **Instrumented demo agent** (#56): `cmd/demo-agent` and `deploy/compose/agent.yaml`.
  - Each agent step is an `invoke_agent` span with fake `chat` model calls carrying `gen_ai.*` attributes.
  - MCP spans nest under the step through `params._meta`.
  - The demo starts it alongside the shell agent, and the smoke test asserts the nesting in Tempo.

- `deploy/compose/screenshot.sh` refreshes the README and wiki screenshots from a running demo. The dashboard image now shows every row (#58).

### Changed
- With `OTEL_METRICS_EXPORTER=prometheus`, metrics now carry a `service_name` label, so gateway routes stay apart on one `/metrics` endpoint.

## [0.5.0] - 2026-10-18

### Added
- Semantic-conventions conformance (#52):
  - `TestEmittedNamesAreInSemconv` checks every emitted span attribute, metric and metric attribute against a pinned registry (GenAI conventions at a commit, core conventions at v1.44.0), with an explicit list of project-defined names.
  - A weekly `semconv-watch` workflow checks against upstream and opens an issue on drift.
  - The policy for upstream renames is in CONTRIBUTING.md, and the draft upstream proposals are in `docs/semconv-proposal.md`.
- `deploy/compose/check-profiles-versions.sh`, run in CI: fails with the exact versions to set when the profiler images drift from the Collector bundled in `grafana/otel-lgtm`. Dependabot now covers the reference stack (#53).

### Fixed
- Console exporters (`OTEL_*_EXPORTER=console`) now write to **stderr** on every platform. On Windows, stdio mode accepts them instead of refusing them (#51).
- Responses are matched to requests by id **value**, not raw bytes. A request with id `1.0` (or `1e0`) answered with `1` used to stay open until the `-session-idle` timeout. The string `"1"` and the number `1` remain distinct, and `jsonrpc.request.id` reports the canonical form (#50).

## [0.4.0]

### Added
- SLSA build provenance (GitHub artifact attestations) for release archives, the image and the chart, verifiable with `gh attestation verify` (#41).
- Dashboard rows for MCP Tasks, client mode and exporter health (failed exports, unparsed frames, dropped telemetry, SDK queue fill), plus `MCPTasksFailing` and `MCPExporterQueueFull` alerts. The smoke test now runs an MCP Task and checks every dashboard query (#40).
- The Helm chart is now installed in a real cluster (kind) on every PR: `deploy/helm/e2e.sh` checks readiness, an MCP session through the Service, the span, and the metrics and pprof ports (#39).
- A wiki (source in `docs/wiki/`, published by `.github/workflows/wiki.yml`): Getting Started, Demo, Kubernetes, Client Mode, Backends, Configuration, Telemetry Reference, Architecture, Profiles, Security, Troubleshooting, Releases, Development.
- One-command demo `deploy/compose/demo.sh` with a simulated agent (`deploy/compose/demo.yaml`) exercising tools, failures, slow calls, prompts, resources, an MCP Task lifecycle, server logging and session rotation, plus dashboard and trace screenshots in the README.

### Fixed
- Docs: the Prometheus exporter binds `localhost` by default. Containers need `OTEL_EXPORTER_PROMETHEUS_HOST=0.0.0.0`.

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

[Unreleased]: https://github.com/firasmosbehi/mcp-exporter/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/firasmosbehi/mcp-exporter/compare/v0.6.1...v0.7.0
[0.6.1]: https://github.com/firasmosbehi/mcp-exporter/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/firasmosbehi/mcp-exporter/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/firasmosbehi/mcp-exporter/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/firasmosbehi/mcp-exporter/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/firasmosbehi/mcp-exporter/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/firasmosbehi/mcp-exporter/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/firasmosbehi/mcp-exporter/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/firasmosbehi/mcp-exporter/releases/tag/v0.1.0
