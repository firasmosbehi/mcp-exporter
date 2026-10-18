#!/bin/sh
# End-to-end smoke test: real MCP server -> mcp-exporter -> otel-lgtm, then
# assert on what reached Prometheus, Tempo and Loki. Traffic and queries run
# from a container on the compose network, so no host ports are needed.
#   deploy/compose/smoke.sh          # run, then tear down
#   KEEP=1 deploy/compose/smoke.sh   # leave the stack up for poking around
#   PROFILES=1 deploy/compose/smoke.sh  # also the profiling overlay (Linux Docker, privileged)
set -eu
cd "$(dirname "$0")"
P=mcp-smoke
N=20
files="-f docker-compose.yaml -f gateway.yaml -f agent.yaml"
export AGENT_TASKS=false # its calls must not change the exact task counts below
[ "${PROFILES:-}" = 1 ] && files="$files -f profiling.yaml"
compose() { docker compose -p "$P" $files "$@"; }
[ "${KEEP:-}" = 1 ] || trap 'compose down -v >/dev/null 2>&1' EXIT

# Every dashboard query, prefixed REQ (must return data here) or ANY (must
# only be valid PromQL; e.g. client-mode and exporter-error panels stay empty).
QUERIES=$(python3 - <<'PY'
import json
rows, out = None, []
for p in json.load(open("../grafana/dashboard.json"))["panels"]:
    if p["type"] == "row":
        rows = p["title"]; continue
    for t in p.get("targets", []):
        req = "REQ" if rows in ("Server", "MCP Tasks") else "ANY"
        out.append(req + " " + t["expr"].replace("$service", ".*").replace("\n", " "))
print("\n".join(out))
PY
)

compose up -d --build --quiet-pull
docker run --rm -i -e N="$N" -e QUERIES="$QUERIES" --network "${P}_default" curlimages/curl:8.11.1 sh <<'IN'
set -eu
fail() { echo "FAIL: $*"; exit 1; }
# retry CMD... until it succeeds (up to ~90s); telemetry arrives asynchronously
retry() { i=0; until "$@"; do i=$((i+1)); [ $i -lt 45 ] || return 1; sleep 2; done; }

U=http://mcp-exporter:8080/mcp
H='-H Content-Type:application/json -H Accept:application/json,text/event-stream'
rpc() { curl -sf $H ${S:+-H "Mcp-Session-Id: $S"} "$U" -d "$1"; }

retry curl -sf http://mcp-exporter:8080/readyz -o /dev/null || fail "exporter not ready"
retry sh -c "curl -sf -D /tmp/h $H $U -d '{\"jsonrpc\":\"2.0\",\"id\":0,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-06-18\",\"capabilities\":{},\"clientInfo\":{\"name\":\"smoke\",\"version\":\"1\"}}}' -o /dev/null" \
  || fail "initialize through the proxy"
S=$(grep -i '^mcp-session-id:' /tmp/h | tr -d '\r' | awk '{print $2}')
[ -n "$S" ] || fail "no Mcp-Session-Id from upstream"
rpc '{"jsonrpc":"2.0","method":"notifications/initialized"}' >/dev/null
i=1; while [ $i -le "$N" ]; do
  rpc "{\"jsonrpc\":\"2.0\",\"id\":$i,\"method\":\"tools/call\",\"params\":{\"name\":\"echo\",\"arguments\":{\"message\":\"m$i\"}}}" | grep -q "Echo: m$i" || fail "echo $i"
  i=$((i+1))
done
rpc '{"jsonrpc":"2.0","id":900,"method":"tools/call","params":{"name":"nope"}}' | grep -q '"isError":true' || fail "unknown tool"
rpc '{"jsonrpc":"2.0","id":901,"method":"bogus/method"}' | grep -q -- '-32601' || fail "unknown method"
# MCP Task: task-augmented call, poll tasks/get until terminal, fetch the result
T=$(rpc '{"jsonrpc":"2.0","id":902,"method":"tools/call","params":{"name":"simulate-research-query","arguments":{"topic":"smoke"},"task":{"ttl":60000}}}' \
  | grep -o '"taskId":"[^"]*"' | head -1 | cut -d'"' -f4)
[ -n "$T" ] || fail "no task created"
i=0; until rpc "{\"jsonrpc\":\"2.0\",\"id\":$((903+i)),\"method\":\"tasks/get\",\"params\":{\"taskId\":\"$T\"}}" | grep -qE '"status":"(completed|failed|cancelled)"'; do
  i=$((i+1)); [ $i -lt 30 ] || fail "task $T never finished"; sleep 1
done
rpc "{\"jsonrpc\":\"2.0\",\"id\":999,\"method\":\"tasks/result\",\"params\":{\"taskId\":\"$T\"}}" >/dev/null
curl -sf -X DELETE -H "Mcp-Session-Id: $S" "$U" -o /dev/null || fail "DELETE session"
echo "traffic ok (session $S)"

PROM=http://lgtm:9090/api/v1/query
q() { curl -s "$PROM" --data-urlencode "query=$1"; }
has() { q "$1" | grep -q "$2"; }
D=mcp_server_operation_duration_seconds_count
retry has "sum($D{gen_ai_tool_name=\"echo\",error_type=\"\"})" "\"$N\"\]" || fail "want $N successful echo calls: $(q "sum($D{gen_ai_tool_name=\"echo\"})")"
retry has "sum($D{gen_ai_tool_name=\"nope\",error_type=\"tool_error\"})" '"1"\]' || fail "unknown tool not recorded as tool_error"
retry has "sum($D{mcp_method_name=\"bogus/method\",rpc_response_status_code=\"-32601\",error_type=\"\"})" '"1"\]' \
  || fail "-32601 must carry rpc.response.status_code but not error.type: $(q "$D{mcp_method_name=\"bogus/method\"}")"
retry has 'sum(mcp_server_session_duration_seconds_count)' '"1"\]' || fail "session end not recorded"
retry has 'sum(mcp_task_duration_seconds_count{mcp_task_status="completed",gen_ai_tool_name="simulate-research-query"})' '"1"\]' \
  || fail "task lifecycle not recorded: $(q 'mcp_task_duration_seconds_count')"
echo "metrics ok"

# Dashboard: every query is valid PromQL; server and task panels have data.
nonempty() { q "$1" | grep -q '"result":\[{'; }
n=0
echo "$QUERIES" | { while read -r kind expr; do
  n=$((n+1))
  q "$expr" | grep -q '"status":"success"' || fail "dashboard query is not valid PromQL: $expr :: $(q "$expr")"
  [ "$kind" = REQ ] && { retry nonempty "$expr" || fail "dashboard panel has no data: $expr"; }
done; echo "dashboard ok ($n queries)"; }

retry sh -c 'curl -s "http://lgtm:3200/api/search?limit=5" --data-urlencode "q={span.gen_ai.tool.name=\"echo\"}" -G | grep -q "tools/call echo"' || fail "no tools/call echo trace in Tempo"
echo "traces ok"
retry sh -c 'curl -s -G http://lgtm:3100/loki/api/v1/query_range --data-urlencode "query={service_name=\"everything\"} | trace_id != \"\"" --data-urlencode limit=1 | grep -q "mcp request"' || fail "no trace-correlated logs in Loki"
echo "logs ok"

# Gateway (#54): each route is its own service in Prometheus and Tempo.
for r in gw-alpha gw-beta; do
  GU=http://mcp-gateway:8080/$r/mcp
  retry curl -sf http://mcp-gateway:8080/$r/readyz -o /dev/null || fail "gateway route $r not ready"
  retry sh -c "curl -sf -D /tmp/gh $H $GU -d '{\"jsonrpc\":\"2.0\",\"id\":0,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-11-25\",\"capabilities\":{},\"clientInfo\":{\"name\":\"smoke-gw\",\"version\":\"1\"}}}' -o /dev/null" \
    || fail "initialize through gateway route $r"
  GS=$(grep -i '^mcp-session-id:' /tmp/gh | tr -d '\r' | awk '{print $2}')
  curl -sf $H -H "Mcp-Session-Id: $GS" $GU -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | grep -q '"tools"' || fail "tools/list through $r"
done
curl -sf http://mcp-gateway:8080/readyz -o /dev/null || fail "gateway aggregate readyz"
for r in gw-alpha gw-beta; do
  retry has "sum($D{service_name=\"$r\",mcp_method_name=\"tools/list\"})" '"1"\]' || fail "no tools/list metric for service $r: $(q "$D{mcp_method_name=\"tools/list\"}")"
  retry sh -c "curl -s -G 'http://lgtm:3200/api/search?limit=5' --data-urlencode 'q={resource.service.name=\"$r\" && span.mcp.method.name=\"tools/list\"}' | grep -q '\"rootServiceName\":\"$r\"'" \
    || fail "no tools/list trace for service $r in Tempo"
done
echo "gateway ok"

# Instrumented agent (#56): its MCP calls nest under its agent steps.
retry sh -c "curl -s -G 'http://lgtm:3200/api/search?limit=5' --data-urlencode 'q={resource.service.name=\"demo-agent\" && span.gen_ai.operation.name=\"invoke_agent\"} >> {resource.service.name=\"everything\" && span.mcp.method.name=\"tools/call\"}' | grep -q '\"rootServiceName\":\"demo-agent\"'" \
  || fail "no trace with an MCP span nested under a demo-agent step"
echo "agent ok"
IN

[ "${PROFILES:-}" = 1 ] && docker run --rm -i --network "${P}_default" curlimages/curl:8.11.1 sh <<'IN'
set -eu
fail() { echo "FAIL: $*"; exit 1; }
retry() { i=0; until "$@"; do i=$((i+1)); [ $i -lt 45 ] || return 1; sleep 2; done; }
services() { # service_name values Pyroscope has seen in the last hour
  now=$(($(date +%s)*1000))
  curl -s -X POST http://lgtm:4040/querier.v1.QuerierService/LabelValues -H 'Content-Type: application/json' \
    -d "{\"name\":\"service_name\",\"start\":$((now-3600000)),\"end\":$now}"
}
has_service() { services | grep -q "\"$1\""; }
retry has_service mcp-exporter || fail "no pprof-scraped profiles for mcp-exporter: $(services)"
echo "profiles (pprof receiver) ok"
# The eBPF profiler only samples processes while they're on-CPU (19 Hz), and an
# idle server can produce no samples at all. Keep traffic flowing while waiting.
H='-H Content-Type:application/json -H Accept:application/json,text/event-stream'
U=http://mcp-exporter:8080/mcp
curl -s -D /tmp/ph $H $U -o /dev/null -d '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"profiles-load","version":"1"}}}'
PS=$(grep -i '^mcp-session-id:' /tmp/ph | tr -d '\r' | awk '{print $2}')
( i=0; while :; do i=$((i+1)); curl -s $H -H "Mcp-Session-Id: $PS" $U -o /dev/null \
    -d "{\"jsonrpc\":\"2.0\",\"id\":$i,\"method\":\"tools/call\",\"params\":{\"name\":\"echo\",\"arguments\":{\"message\":\"load $i\"}}}"; done ) &
LOAD=$!
retry has_service everything || { kill $LOAD; fail "no eBPF profiles for the MCP server (service everything): $(services)"; }
kill $LOAD
echo "profiles (eBPF) ok"
IN
echo "PASS: compose smoke test"
