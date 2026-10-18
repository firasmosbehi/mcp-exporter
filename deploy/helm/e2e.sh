#!/bin/sh
# Installs the chart into the cluster KUBECONFIG points at (kind in CI) and
# checks it end to end: pod Ready (readiness = upstream reachable), an MCP
# session through the Service, a span in the exporter's log, and the metrics
# and pprof ports. The image must already be loaded into the cluster.
#   IMAGE=mcp-exporter:e2e deploy/helm/e2e.sh
set -eu
cd "$(dirname "$0")"
REL=e2e NS=mcp-e2e IMAGE=${IMAGE:-mcp-exporter:e2e}
fail() { echo "FAIL: $*"; kubectl -n $NS get pods -o wide || true; kubectl -n $NS logs deploy/$REL --all-containers --tail 30 || true; exit 1; }

kubectl create namespace $NS --dry-run=client -o yaml | kubectl apply -f - >/dev/null
helm upgrade --install $REL ./mcp-exporter -n $NS --wait --timeout 8m \
  --set exporter.image="${IMAGE%:*}" --set exporter.tag="${IMAGE##*:}" \
  --set exporter.prometheus.enabled=true --set exporter.pprof.enabled=true \
  --set exporter.env.OTEL_TRACES_EXPORTER=console --set exporter.env.OTEL_LOGS_EXPORTER=none \
  || fail "chart did not become Ready"
echo "installed and Ready"

POD_IP=$(kubectl -n $NS get pod -l app.kubernetes.io/instance=$REL -o jsonpath='{.items[0].status.podIP}')
kubectl -n $NS run mcp-client --rm -i --restart=Never --quiet --image=curlimages/curl:8.11.1 --env=POD_IP="$POD_IP" --command -- sh -c '
set -eu
U=http://e2e/mcp
H="-H Content-Type:application/json -H Accept:application/json,text/event-stream"
curl -sf -D /tmp/h $H $U -o /dev/null -d "{\"jsonrpc\":\"2.0\",\"id\":0,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-11-25\",\"capabilities\":{},\"clientInfo\":{\"name\":\"e2e\",\"version\":\"1\"}}}"
S=$(grep -i "^mcp-session-id:" /tmp/h | tr -d "\r" | cut -d" " -f2)
curl -sf $H -H "Mcp-Session-Id: $S" $U -o /dev/null -d "{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}"
curl -sf $H -H "Mcp-Session-Id: $S" $U -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"echo\",\"arguments\":{\"message\":\"from-k8s\"}}}" | grep -q "from-k8s"
echo "mcp session through the Service ok"
curl -sf http://e2e:9464/metrics | grep -q "^mcp_server_operation_duration_seconds_count" && echo "metrics port ok"
curl -sf "http://$POD_IP:6060/debug/pprof/cmdline" | grep -q -- "-pprof" && echo "pprof port ok"
' || fail "client checks"

i=0
until kubectl -n $NS logs deploy/$REL -c mcp-exporter | grep -q '"Name":"tools/call echo"'; do
  i=$((i+1)); [ $i -lt 15 ] || fail "no tools/call echo span in the exporter log"; sleep 2
done
echo "span in exporter log ok"

# Gateway mode: no sidecar, one route to the release above (via its Service).
GW=e2e-gw
helm upgrade --install $GW ./mcp-exporter -n $NS --wait --timeout 4m \
  --set exporter.image="${IMAGE%:*}" --set exporter.tag="${IMAGE##*:}" \
  --set 'gateway.routes[0].name=everything' --set "gateway.routes[0].url=http://$REL.$NS.svc" \
  --set exporter.env.OTEL_TRACES_EXPORTER=console --set exporter.env.OTEL_LOGS_EXPORTER=none \
  || fail "gateway release did not become Ready"
[ "$(kubectl -n $NS get deploy $GW -o jsonpath='{.spec.template.spec.containers[*].name}')" = mcp-exporter ] || fail "gateway mode must not deploy a server sidecar"
kubectl -n $NS run mcp-gw-client --rm -i --restart=Never --quiet --image=curlimages/curl:8.11.1 --command -- sh -c '
set -eu
U=http://e2e-gw/everything/mcp
H="-H Content-Type:application/json -H Accept:application/json,text/event-stream"
curl -sf -D /tmp/h $H $U -o /dev/null -d "{\"jsonrpc\":\"2.0\",\"id\":0,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-11-25\",\"capabilities\":{},\"clientInfo\":{\"name\":\"e2e-gw\",\"version\":\"1\"}}}"
S=$(grep -i "^mcp-session-id:" /tmp/h | tr -d "\r" | cut -d" " -f2)
curl -sf $H -H "Mcp-Session-Id: $S" $U -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\"}" | grep -q "\"tools\""
curl -sf http://e2e-gw/everything/readyz -o /dev/null
echo "mcp session through the gateway ok"
' || fail "gateway client checks"
i=0
until kubectl -n $NS logs deploy/$GW | grep '"Name":"tools/list"' | grep -q '"Value":"everything"'; do
  i=$((i+1)); [ $i -lt 15 ] || fail "no tools/list span with service.name=everything in the gateway log"; sleep 2
done
echo "gateway span ok"
echo "PASS: helm chart e2e"
