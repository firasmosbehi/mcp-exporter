#!/bin/sh
# One-command demo: MCP server + mcp-exporter + Grafana stack + a simulated agent.
#   deploy/compose/demo.sh              start (add PROFILES=1 for CPU profiles, Linux Docker only)
#   deploy/compose/demo.sh down         stop and remove everything
set -eu
cd "$(dirname "$0")"
files="-f docker-compose.yaml -f demo.yaml"
[ "${PROFILES:-}" = 1 ] && files="$files -f profiling.yaml"
if [ "${1:-}" = down ]; then
  docker compose -p mcp-demo $files -f profiling.yaml down -v
  exit
fi
docker compose -p mcp-demo $files up -d --build
cat <<MSG

  mcp-exporter demo is running. Traffic starts within a minute.

  Dashboard   http://localhost:3000/d/mcp-exporter     (Grafana, no login)
  Traces      http://localhost:3000/explore            → Tempo, query {span.gen_ai.tool.name != ""}
  Logs        http://localhost:3000/explore            → Loki, {service_name="everything"}
  MCP         http://localhost:8080/mcp                (point your own client here too)

  Agent log:  docker compose -p mcp-demo logs -f demo-agent
  Stop:       $0 down
MSG
