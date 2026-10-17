# Backends

Every backend takes **only `OTEL_*` environment variables**. Recipes for Grafana Cloud, Honeycomb, Datadog (agentless,
HTTP only, delta metrics) and SigNoz, plus what to expect in GenAI "LLM views", are maintained in the repository:

**→ [docs/backends.md](https://github.com/firasmosbehi/mcp-exporter/blob/main/docs/backends.md)**

The short version, for any OTLP backend or Collector:
```sh
export OTEL_SERVICE_NAME=weather-mcp
export OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318   # or :4317 with OTEL_EXPORTER_OTLP_PROTOCOL=grpc
export OTEL_EXPORTER_OTLP_HEADERS="authorization=Bearer …"      # whatever your backend requires
```

To pull metrics with Prometheus instead, set `OTEL_METRICS_EXPORTER=prometheus` and scrape `:9464/metrics`.
It binds **`localhost` by default**, so in a container also set `OTEL_EXPORTER_PROMETHEUS_HOST=0.0.0.0`
(the Helm chart does this when `exporter.prometheus.enabled`). `OTEL_EXPORTER_PROMETHEUS_PORT` changes the port.
