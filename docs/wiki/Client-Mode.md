# Client Mode

`-role client` observes from the **agent's side**. Use it for remote MCP servers you don't run: SaaS endpoints,
other teams' servers, or anything you can't put a sidecar next to.

| | received by the observed side | sent by the observed side |
|---|---|---|
| server role (default) | client → server requests: `SERVER` spans | server → client requests: `CLIENT` spans |
| client role | server → client requests (sampling, elicitation): `SERVER` spans | agent → server requests: `CLIENT` spans with `server.address`/`server.port` |
| duration metric | `mcp.server.operation.duration` | `mcp.client.operation.duration` |
| JSON-RPC errors | caller codes (-32700, -32600, -32601, -32602, -32002) are not errors | every code is an error |

Session and in-flight instruments are named for the role: `mcp.client.session.duration`,
`mcp.client.sessions.active` and `mcp.client.requests.inflight`.

## Recommended: wrap `mcp-remote`

```jsonc
"deepwiki": {
  "command": "mcp-exporter",
  "args": ["stdio", "-role", "client", "--", "npx", "-y", "mcp-remote", "https://mcp.deepwiki.com/mcp"],
  "env": { "OTEL_SERVICE_NAME": "deepwiki" }
}
```

[`mcp-remote`](https://www.npmjs.com/package/mcp-remote) bridges stdio to a remote server and runs the OAuth flow against the
**real** server URL, so tokens are issued for the right audience. This form has been tested against
`mcp.deepwiki.com`: the tool list arrived intact, the spans were `CLIENT` spans with `mcp.server.name=DeepWiki`, and only
`mcp.client.*` metrics were emitted.

## HTTP form

```sh
mcp-exporter http -role client -listen 127.0.0.1:8080 -upstream https://mcp.example.com
# the agent connects to http://127.0.0.1:8080/mcp
```

Headers, including `Authorization`, are forwarded and never recorded. **OAuth caveat:** the agent sees the proxy's URL.
The MCP authorization spec requires RFC 8707 resource indicators, and servers that enforce token audience
reject tokens minted for the proxy URL. API-key or static bearer auth works fine. For OAuth servers, use the `mcp-remote` form.
