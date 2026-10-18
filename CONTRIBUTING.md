# Contributing

Thanks for helping make MCP servers observable.

## Before you start

- **Bugs:** open an issue with the bug template. Exporter version, transport (stdio/HTTP) and a redacted frame sample make most bugs quick to fix.
- **Features:** open an issue first for anything bigger than a small fix, so we can agree on the shape before you write code. Check the [milestones](https://github.com/firasmosbehi/mcp-exporter/milestones) for planned work.
- **Telemetry names:** spans, attributes and metrics follow the [OpenTelemetry MCP semantic conventions](https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/mcp.md). New names need a reason the conventions don't cover the case, and use the `mcp_exporter.` prefix for the exporter's own telemetry.

## Development

Requires Go (version in `go.mod`), plus Docker for the end-to-end test.

```sh
go test -race ./...                      # unit tests
go test ./internal/observe -bench .      # per-request overhead
go test ./internal/jsonrpc -run '^$' -fuzz FuzzParse -fuzztime 1m   # fuzz (also FuzzSSEObserver, FuzzGunzip, FuzzFrame)
gofmt -l . && go vet ./...               # what CI checks
deploy/compose/smoke.sh                  # end-to-end: compose stack + assertions (needs Docker)
deploy/helm/e2e.sh                       # Helm chart in the cluster KUBECONFIG points at (CI uses kind)
```

Layout:

| Path | What |
|---|---|
| `internal/jsonrpc` | JSON-RPC parsing and `params._meta` editing |
| `internal/observe` | OTel setup and the `Tracker` (frames → spans, metrics, logs) |
| `internal/proxy` | stdio and Streamable HTTP transports |
| `cmd/mcp-exporter` | CLI |
| `deploy/` | compose stack, Helm chart, Grafana dashboard, alerts |

## Semantic convention changes

The MCP conventions are still **Development**. `TestEmittedNamesAreInSemconv` checks every span attribute, metric and
metric attribute we emit against a pinned copy of the registry in `internal/observe/testdata/semconv`
(GenAI conventions at a commit, core conventions at a release). The weekly `semconv-watch` workflow runs the same
test against upstream and opens an issue on drift.

- **Adding a name:** use the semconv name. If none exists, add it to `projectDefined`, mark it † in the wiki's Telemetry Reference, and
  consider adding it to [docs/semconv-proposal.md](docs/semconv-proposal.md).
- **Upstream renames a name we emit:** for at least one minor release, emit both names. Gate it with
  `OTEL_SEMCONV_STABILITY_OPT_IN=mcp` (new only) / `mcp/dup` (both); the default is old plus new. Then switch the default, and
  add a CHANGELOG migration note with PromQL/TraceQL before-and-after examples.
- **Upstream adopts a project-defined name:** the test says so. Check the definition matches, then remove it from `projectDefined` and the †.
- **Bumping the pin:** edit `internal/observe/testdata/semconv/SOURCE`, run `sync.sh`, then `go test ./internal/observe -run Semconv`.

## Pull requests

- Keep PRs focused, and add a test that fails without your change.
- Never forward bytes you didn't receive. The proxy must stay transparent: observe, don't rewrite (the only exception is the opt-out `_meta` trace context injection).
- Don't add dependencies for what the standard library or OTel SDK already does.
- Update `CHANGELOG.md` under **Unreleased** for user-visible changes.
- By contributing you agree your work is licensed under the [Apache License 2.0](LICENSE).

## Releases

Maintainers tag `vX.Y.Z` on `main`. The release workflow builds signed binaries, a multi-arch image and the Helm chart.
