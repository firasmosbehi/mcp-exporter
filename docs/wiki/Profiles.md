# Profiles

OTLP Profiles are in **public Alpha**, and the wire format still changes between Collector releases. Go has no profiles SDK yet,
so both sources run as Collectors and send OTLP profiles to a backend that accepts them (Pyroscope, or a Collector
started with `--feature-gates=service.profilesSupport`).

| Source | Profiles | How |
|---|---|---|
| **pprof receiver** | the exporter itself (CPU, heap, goroutines) | `mcp-exporter -pprof :6060`, scraped at `/debug/pprof/profile?seconds=10` |
| **eBPF profiler** | every process on a Linux host, **MCP servers in any language** | privileged, host PID; labels processes by their `OTEL_SERVICE_NAME` |

## Try it

```sh
PROFILES=1 deploy/compose/demo.sh       # Grafana → Explore → Profiles (Pyroscope)
```
[`deploy/compose/profiling.yaml`](https://github.com/firasmosbehi/mcp-exporter/blob/main/deploy/compose/profiling.yaml) holds both Collector configs, and
CI's end-to-end test checks that profiles from both arrive.

## Things to know

- **Keep versions aligned.** `grafana/otel-lgtm` 0.11 (Collector 0.134) rejected profiles from Collector 0.163 with
  `proto: wrong wireType`. The repo pins the profilers to **0.161.0**, matching `otel-lgtm:0.35.0`.
- **`-pprof` exposes process internals.** It's off by default, never on the proxy port, and should be bound to localhost or
  pod-internal addresses (Helm: `exporter.pprof.enabled` plus a NetworkPolicy).
- **Correlation with traces** is by service, instance and time window. Samples aren't tagged with span ids.
- **eBPF in Kubernetes:** [`deploy/kubernetes/ebpf-profiler.yaml`](https://github.com/firasmosbehi/mcp-exporter/blob/main/deploy/kubernetes/ebpf-profiler.yaml)
  (DaemonSet with `k8s_attributes`). Point its exporter at your profiles backend.
