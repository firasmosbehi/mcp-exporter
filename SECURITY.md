# Security policy

`mcp-exporter` sits in the path of MCP traffic, which can carry credentials and private data, so security reports are a priority.

## Reporting a vulnerability

Please **do not open a public issue**. Report privately through
[GitHub security advisories](https://github.com/firasmosbehi/mcp-exporter/security/advisories/new).

Include the version, mode (stdio/HTTP), configuration flags and steps to reproduce. You can expect an acknowledgement within 3 business days and a fix or mitigation plan within 30 days for confirmed issues.

## Supported versions

Until 1.0, only the latest minor release receives security fixes.

## Scope

In scope, for example:
- telemetry leaking secrets (headers, tokens, unredacted payloads, raw session ids)
- the proxy altering, dropping or injecting MCP traffic beyond documented `_meta` trace context injection
- crashes or unbounded memory growth triggered by a client or server

Release artifacts are signed with cosign (keyless). Verification instructions are in the README.
