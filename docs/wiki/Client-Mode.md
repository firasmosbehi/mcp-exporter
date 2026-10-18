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

Headers, including `Authorization`, are forwarded and never recorded. API-key and static bearer auth work as is.
For OAuth servers, see below.

## OAuth through the HTTP form

**Short answer:** an off-the-shelf agent pointed at the proxy URL can't complete OAuth against a server that follows the MCP
authorization spec, and nothing the proxy can do changes that. Use the `mcp-remote` form above. If you build your own agent on the
TypeScript SDK, there is a one-method workaround (see "Your own agent" below).

**Why** (MCP authorization 2025-11-25, RFC 9728, RFC 8707):

1. The agent connects to the proxy, `http://127.0.0.1:8080/mcp`. The server answers `401` with
   `WWW-Authenticate: Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`.
2. RFC 9728 §3.3: when metadata comes from a `resource_metadata` URL, its `resource` value **MUST be identical to the URL
   the client used to make the request**, or the client **MUST NOT** use it. Here `resource` is `https://mcp.example.com/mcp`,
   while the client used the proxy URL. The TypeScript SDK (1.32) enforces this as an origin and path-prefix check, and fails with
   `Protected resource … does not match expected …`.
3. Suppose the proxy rewrote the metadata to say `resource=http://127.0.0.1:8080/mcp`. The agent would then ask the authorization server
   for a token with that `resource` (RFC 8707). Either the authorization server rejects the unknown resource (`invalid_target`), or it
   issues a token with that audience. Then the MCP server must reject it: servers **MUST** only accept tokens issued for themselves.
   Making that work means changing the server's audience configuration. If you control the server, run the exporter
   next to it in the default server role instead.
4. Forward proxying (`HTTPS_PROXY`) keeps the real URL in the agent. But HTTPS through a forward proxy is a `CONNECT` tunnel, so seeing the
   JSON-RPC inside means TLS interception with a locally trusted CA. That's out of scope for this project.

So the URL the agent believes it is talking to must be the real server's URL. The `mcp-remote` form satisfies this: the agent
speaks stdio, and `mcp-remote` runs OAuth against the real URL. This form is tested.

**Your own agent (TypeScript SDK):** `OAuthClientProvider.validateResourceURL(serverUrl, resource)` replaces the SDK's
resource check. Return the real server's canonical URL, and the token is requested for the real audience while the transport
goes through the proxy. The proxy forwards `Authorization` unchanged:

```ts
const provider: OAuthClientProvider = {
  // ...your usual provider methods...
  async validateResourceURL(_serverUrl, resource) {
    const real = "https://mcp.example.com/mcp";
    if (resource && resource !== real) throw new Error(`unexpected resource ${resource}`);
    return new URL(real);
  },
};
new StreamableHTTPClientTransport(new URL("http://127.0.0.1:8080/mcp"), { authProvider: provider });
```

This override is read from SDK 1.32.1's source and has not been tested end to end against an OAuth server. Desktop agents
generally don't expose a resource override, so for them use `mcp-remote`.
