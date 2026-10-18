#!/bin/sh
# Refreshes docs/images/{dashboard,agent-trace}.png from a running demo
# (deploy/compose/demo.sh; let it run ~10 minutes so every panel has data).
#   deploy/compose/screenshot.sh
# Headless Chromium runs on the demo network. Grafana's root_url is
# localhost:3000, so localhost is mapped to lgtm; the locale is forced to en-US
# because the container's default (en-US@posix) breaks Grafana.
set -eu
cd "$(dirname "$0")"
OUT=$(cd ../../docs/images && pwd)
NAME=mcp-screenshot
docker rm -f $NAME >/dev/null 2>&1 || true
docker create --name $NAME --network mcp-demo_default mcr.microsoft.com/playwright/python:v1.55.0-noble \
  sh -c 'pip install -q --break-system-packages playwright==1.55.0 >/dev/null 2>&1 && mkdir -p /out && python3 /shoot.py' >/dev/null
PY_FILE=$(mktemp)
trap 'docker rm -f $NAME >/dev/null 2>&1; rm -f "$PY_FILE"' EXIT
cat > "$PY_FILE" <<'PY'
import base64, json, urllib.parse, urllib.request
from playwright.sync_api import sync_playwright

G = "http://localhost:3000"  # mapped to lgtm below
auth = {"Authorization": "Basic " + base64.b64encode(b"admin:admin").decode()}

def get(url):
    return json.load(urllib.request.urlopen(urllib.request.Request(url, headers=auth)))

tempo = {d["type"]: d["uid"] for d in get("http://lgtm:3000/api/datasources")}["tempo"]
# An agent step that ran an MCP Task: model calls, tool calls and linked task polls.
q = '{resource.service.name="demo-agent"} >> {span.gen_ai.tool.name="simulate-research-query"}'
traces = get("http://lgtm:3200/api/search?limit=1&q=" + urllib.parse.quote(q))["traces"]
if not traces:
    raise SystemExit("no demo-agent trace with an MCP Task yet; let the demo run longer")
tid = traces[0]["traceID"]

with sync_playwright() as p:
    b = p.chromium.launch(args=["--host-resolver-rules=MAP localhost lgtm"])
    # Tall viewports rather than full_page: Grafana only loads panels in view.
    pg = b.new_page(viewport={"width": 1600, "height": 2450}, locale="en-US", color_scheme="dark")
    pg.set_extra_http_headers(auth)
    pg.goto(G + "/d/mcp-exporter?orgId=1&from=now-15m&to=now&kiosk", wait_until="networkidle")
    pg.wait_for_selector("text=Exporter health", timeout=30000)
    pg.wait_for_timeout(8000)
    pg.screenshot(path="/out/dashboard.png")
    pg.set_viewport_size({"width": 1600, "height": 1500})

    panes = {"a": {"datasource": tempo, "queries": [{"refId": "A", "datasource": {"type": "tempo", "uid": tempo},
             "queryType": "traceql", "query": tid}], "range": {"from": "now-1h", "to": "now"}}}
    pg.goto(G + "/explore?schemaVersion=1&orgId=1&panes=" + urllib.parse.quote(json.dumps(panes)), wait_until="networkidle")
    pg.wait_for_selector("text=invoke_agent demo-agent", timeout=30000)
    pg.wait_for_timeout(4000)
    pg.screenshot(path="/out/agent-trace.png")
    b.close()
print("captured trace", tid)
PY
docker cp "$PY_FILE" $NAME:/shoot.py
docker start -a $NAME
docker cp $NAME:/out/dashboard.png "$OUT/dashboard.png"
docker cp $NAME:/out/agent-trace.png "$OUT/agent-trace.png"
echo "updated $OUT/dashboard.png and $OUT/agent-trace.png"
