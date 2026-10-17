FROM grafana/otel-lgtm:0.11.10
COPY grafana/dashboard.json /otel-lgtm/mcp-dashboard.json
COPY compose/grafana-provisioning.yaml /otel-lgtm/grafana/conf/provisioning/dashboards/mcp.yaml
