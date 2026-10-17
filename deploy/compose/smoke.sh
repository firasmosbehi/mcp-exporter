#!/bin/sh
# End-to-end smoke test: real MCP server -> mcp-exporter -> otel-lgtm, then
# assert on what reached Prometheus, Tempo and Loki. Traffic and queries run
# from a container on the compose network, so no host ports are needed.
#   deploy/compose/smoke.sh          # run, then tear down
#   KEEP=1 deploy/compose/smoke.sh   # leave the stack up for poking around
set -eu
cd "$(dirname "$0")"
P=mcp-smoke
N=20
compose() { docker compose -p "$P" -f docker-compose.yaml "$@"; }
[ "${KEEP:-}" = 1 ] || trap 'compose down -v >/dev/null 2>&1' EXIT

compose up -d --build --quiet-pull
docker run --rm -i -e N="$N" --network "${P}_default" curlimages/curl:8.11.1 sh <<'IN'
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
echo "metrics ok"

retry sh -c 'curl -s "http://lgtm:3200/api/search?limit=5" --data-urlencode "q={span.gen_ai.tool.name=\"echo\"}" -G | grep -q "tools/call echo"' || fail "no tools/call echo trace in Tempo"
echo "traces ok"
retry sh -c 'curl -s -G http://lgtm:3100/loki/api/v1/query_range --data-urlencode "query={service_name=\"everything\"} | trace_id != \"\"" --data-urlencode limit=1 | grep -q "mcp request"' || fail "no trace-correlated logs in Loki"
echo "logs ok"
IN
echo "PASS: compose smoke test"
