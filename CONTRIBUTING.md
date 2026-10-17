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
```

Layout:

| Path | What |
|---|---|
| `internal/jsonrpc` | JSON-RPC parsing and `params._meta` editing |
| `internal/observe` | OTel setup and the `Tracker` (frames → spans, metrics, logs) |
| `internal/proxy` | stdio and Streamable HTTP transports |
| `cmd/mcp-exporter` | CLI |
| `deploy/` | compose stack, Helm chart, Grafana dashboard, alerts |

## Pull requests

- Keep PRs focused, and add a test that fails without your change.
- Never forward bytes you didn't receive. The proxy must stay transparent: observe, don't rewrite (the only exception is the opt-out `_meta` trace context injection).
- Don't add dependencies for what the standard library or OTel SDK already does.
- Update `CHANGELOG.md` under **Unreleased** for user-visible changes.
- By contributing you agree your work is licensed under the [Apache License 2.0](LICENSE).

## Releases

Maintainers tag `vX.Y.Z` on `main`. The release workflow builds signed binaries, a multi-arch image and the Helm chart.
