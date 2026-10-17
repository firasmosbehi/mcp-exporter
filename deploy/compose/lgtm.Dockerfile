FROM grafana/otel-lgtm:0.35.0
COPY grafana/dashboard.json /otel-lgtm/mcp-dashboard.json
COPY compose/grafana-provisioning.yaml /otel-lgtm/grafana/conf/provisioning/dashboards/mcp.yaml
