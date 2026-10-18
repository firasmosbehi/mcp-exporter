# Kubernetes

The Helm chart runs your MCP server with the exporter as a **sidecar** in the same pod. The Service targets the exporter,
and the exporter proxies to the server over `127.0.0.1`.

```sh
helm install weather oci://ghcr.io/firasmosbehi/charts/mcp-exporter --version <version> \
  --set server.image=ghcr.io/acme/weather-mcp:1.2.0 \
  --set server.port=3000 \
  --set exporter.env.OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector.observability:4318
```

## Gateway mode

To put one exporter in front of MCP servers that already run elsewhere in the cluster, set `gateway.routes`. The chart then deploys
only the exporter, with no server sidecar:

```yaml
gateway:
  routes:
    - {name: github, url: "http://github-mcp.tools:3000"}
    - {name: jira, url: "http://jira-mcp.tools:3000"}
```

Agents connect to `http://<release>/github/mcp`. Each route is exported as its own `service.name` (`github`, `jira`), and request ids
and sessions are tracked separately per upstream. The readiness probe (`/readyz`) waits for every upstream, while `/<name>/readyz` checks one.
The release's own `OTEL_SERVICE_NAME` labels only the exporter's self-telemetry, such as export errors.
Session affinity applies as in sidecar mode.

## Values

| Value | Default | Notes |
|---|---|---|
| `server.image`, `command`, `args`, `env`, `port`, `resources` | server-everything on 3001 | your MCP server; must speak Streamable HTTP on `server.port` |
| `gateway.routes` | `[]` | `[{name, url}]`; when set, gateway mode without the server sidecar |
| `exporter.image` / `exporter.tag` | ghcr image / chart `appVersion` | |
| `exporter.port` | `8080` | Service `targetPort` |
| `exporter.env` | OTLP endpoint + parent-based sampler | any `OTEL_*` variable, passed verbatim |
| `exporter.extraArgs` | `[]` | e.g. `["-capture-payloads", "-capture-tools=search"]` |
| `exporter.prometheus.enabled` / `port` | `false` / `9464` | pull metrics instead of pushing them; adds a `metrics` port |
| `exporter.pprof.enabled` / `port` | `false` / `6060` | Go pprof for a Collector pprof receiver; restrict with a NetworkPolicy |
| `serviceMonitor.enabled`, `interval`, `labels` | `false` | Prometheus Operator; requires `exporter.prometheus.enabled` |
| `service.type` / `port` | `ClusterIP` / `80` | |
| `replicas`, `podAnnotations`, `nodeSelector`, `tolerations`, `affinity` | | |

The chart sets these for you:
- `OTEL_SERVICE_NAME` = the release name;
- `OTEL_RESOURCE_ATTRIBUTES` with `k8s.namespace.name` and `k8s.pod.name`;
- a read-only root filesystem, non-root user and no capabilities on the exporter container;
- a **readiness probe on `/readyz`**: the pod leaves the Service while the MCP server container is down or still starting;
- a liveness probe on `/healthz`.

## Sessions and scaling

Streamable HTTP sessions (`Mcp-Session-Id`) are held by the MCP *server*, so with `replicas > 1` you need
session affinity at the load balancer, as you would without the exporter. The exporter keeps per-pod state only
for open requests and sessions. Idle entries are swept after `-session-idle` (default 30m).

## Recommended Collector setup

Send to an in-cluster OpenTelemetry Collector. Use `k8sattributes` (`k8s_attributes` in newer releases) for pod metadata,
`tail_sampling` to keep every trace with an `error.type` while sampling the rest, and fan out to your backends.

## Profiles

[`deploy/kubernetes/ebpf-profiler.yaml`](https://github.com/firasmosbehi/mcp-exporter/blob/main/deploy/kubernetes/ebpf-profiler.yaml) runs the
OpenTelemetry eBPF profiler as a DaemonSet (privileged, host PID). It labels each process with its `OTEL_SERVICE_NAME`,
so set that variable on your MCP server container. See [[Profiles]].
