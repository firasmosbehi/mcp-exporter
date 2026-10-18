# Security

`mcp-exporter` sits in the path of MCP traffic, which can carry credentials and private data.

## What is never exported

- **HTTP headers:** `Authorization`, cookies and all other headers are forwarded but never recorded.
- **Session ids:** `Mcp-Session-Id` is exported only as a truncated SHA-256, so it stays correlatable but unusable.
- **Payloads:** tool arguments and results aren't recorded unless you pass `-capture-payloads`.

## When payloads are captured

Captured payloads and server log data go through two redaction passes, then truncation to `-max-payload`:
1. **By key:** values under object keys matching `-redact-keys` (default `(?i)pass|token|secret|key|auth|credential|cookie|session`)
   become `[REDACTED]`, at any depth.
2. **By value:** JWTs, `Bearer …` tokens, `sk-…` keys, GitHub `gh[pousr]_…` tokens, Slack `xox…` tokens and AWS access
   key ids are scrubbed inside *any* string, whatever its key.

Use `-capture-tools search,lookup` to capture only the tools you've reviewed.

## Hardening

- **TLS:** `-tls-cert`/`-tls-key` on the listener; add `-tls-client-ca` for mTLS. For the backend, use the `OTEL_EXPORTER_OTLP_*CERTIFICATE*` variables.
- **Container:** a distroless image running as non-root `65532`. The Helm chart sets a read-only root filesystem, drops all capabilities and disables privilege escalation.
- **Robustness:** untrusted-input parsers are fuzz-tested in CI, request bodies are capped at 16 MiB, and gzip decompression is bounded (zip-bomb safe).
- **Supply chain:** releases are keyless-cosign signed (binaries via `checksums.txt`, the image and the chart), ship SBOMs, and carry SLSA build provenance (`gh attestation verify`). See [[Releases]].

## Known advisories

| Affected | Issue | Fixed in |
|---|---|---|
| ≤ 0.3.0 | a request with `"params": {"_meta": null}` (or `"params": null`) crashed the exporter in stdio mode while injecting trace context; workaround `-inject-context=false` | **0.3.1** |

## Reporting

Report privately through [GitHub security advisories](https://github.com/firasmosbehi/mcp-exporter/security/advisories/new), never in public issues
([SECURITY.md](https://github.com/firasmosbehi/mcp-exporter/blob/main/SECURITY.md)).
