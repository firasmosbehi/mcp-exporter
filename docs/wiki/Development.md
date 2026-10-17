# Development

```sh
go test -race ./...                                      # unit and binary tests
go test ./internal/observe -bench .                      # overhead
go test ./internal/jsonrpc -run '^$' -fuzz FuzzParse -fuzztime 1m   # also FuzzSSEObserver, FuzzGunzip (proxy), FuzzFrame (observe)
deploy/compose/smoke.sh                                  # end-to-end: real server → exporter → Prometheus/Tempo/Loki assertions
PROFILES=1 deploy/compose/smoke.sh                       # + profiles (Linux Docker)
gofmt -l . && go vet ./...
```

## CI

| Job | What |
|---|---|
| `test` | gofmt, vet, `go test -race`, 20 s of fuzzing per target, govulncheck, helm lint |
| `windows` | `go test ./...` on windows-latest, including the console-exporter refusal |
| `e2e` | `smoke.sh` with `PROFILES=1`; dumps container logs on failure |

## Releasing

1. Move the CHANGELOG `Unreleased` section to `## [x.y.z]`, bump the version references in the README, and merge.
2. `git tag -a vx.y.z -m vx.y.z && git push origin vx.y.z`
3. The release workflow runs the full CI, then goreleaser (binaries, image, SBOMs, signatures), then pushes and signs the chart.
4. Verify as on [[Releases]]. Try risky pipeline changes on a `-rcN` tag first.

## Conventions

- **Telemetry names:** follow the OTel MCP semantic conventions. New, non-semconv names must be documented as such
  ([[Telemetry Reference]] marks them †).
- **Transparency:** the proxy observes and doesn't rewrite; every exception is listed in [[Architecture]].
- **Tests:** every change carries a test that fails without it. Parsers get fuzz seeds.
- **Wiki:** this wiki is generated from `docs/wiki/` and published on every push to `main`.

See also [CONTRIBUTING.md](https://github.com/firasmosbehi/mcp-exporter/blob/main/CONTRIBUTING.md).
