package observe

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// projectDefined lists names this project emits that the semantic conventions
// don't define (marked † in the wiki's Telemetry Reference). Anything else we
// emit must exist in the pinned registry under testdata/semconv (#52).
var projectDefined = map[string]bool{
	// span attributes
	"mcp.server.name": true, "mcp.client.name": true, "mcp.task.id": true, "mcp.task.status": true,
	// metric attributes
	"mcp.direction": true,
	// metrics
	"mcp.message.size": true, "mcp.task.duration": true,
	"mcp.server.sessions.active": true, "mcp.server.requests.inflight": true,
	"mcp.client.sessions.active": true, "mcp.client.requests.inflight": true,
	"mcp_exporter.parse.errors": true,
}

var (
	attrKey    = regexp.MustCompile(`(?m)^\s*- (?:key|id): ([a-z0-9_]+(?:\.[a-z0-9_]+)+)\s*$`) // definition/2 "key", legacy "id"
	metricName = regexp.MustCompile(`(?m)^\s*(?:- name|metric_name): ([a-z0-9_]+(?:\.[a-z0-9_]+)+)\s*$`)
)

// registryNames reads every attribute and metric name from the registry
// files in dir (SEMCONV_DIR overrides, to check against upstream).
func registryNames(t *testing.T) map[string]bool {
	dir := os.Getenv("SEMCONV_DIR")
	if dir == "" {
		dir = "testdata/semconv"
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if len(files) == 0 {
		t.Fatalf("no registry files in %s (run testdata/semconv/sync.sh)", dir)
	}
	names := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range []*regexp.Regexp{attrKey, metricName} {
			for _, m := range re.FindAllSubmatch(b, -1) {
				names[string(m[1])] = true
			}
		}
	}
	return names
}

// emittedNames drives every telemetry path the Tracker has and returns all
// span attribute keys, metric names and metric attribute keys it produced.
func emittedNames(t *testing.T) map[string]bool {
	_, spans, reader := setup(t)
	run := func(tr *Tracker, frames ...[2]string) {
		for _, f := range frames {
			dir := ClientToServer
			if f[0] == "s" {
				dir = ServerToClient
			}
			tr.Frame(context.Background(), dir, "s", "sess", []byte(f[1]))
		}
		tr.Close()
	}
	server := []([2]string){
		{"c", `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"c"}}}`},
		{"s", `{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":"2025-11-25","serverInfo":{"name":"s"}}}`},
		{"c", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"t","arguments":{"q":1},"task":{}}}`},
		{"s", `{"jsonrpc":"2.0","id":1,"result":{"task":{"taskId":"T","status":"working"}}}`},
		{"c", `{"jsonrpc":"2.0","id":2,"method":"tasks/get","params":{"taskId":"T"}}`},
		{"s", `{"jsonrpc":"2.0","id":2,"result":{"taskId":"T","status":"completed"}}`},
		{"c", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"t","arguments":{}}}`},
		{"s", `{"jsonrpc":"2.0","id":3,"result":{"content":[],"isError":true}}`},
		{"c", `{"jsonrpc":"2.0","id":4,"method":"prompts/get","params":{"name":"p"}}`},
		{"s", `{"jsonrpc":"2.0","id":4,"error":{"code":-32602,"message":"bad"}}`},
		{"c", `{"jsonrpc":"2.0","id":5,"method":"resources/read","params":{"uri":"x://y"}}`},
		{"s", `{"jsonrpc":"2.0","id":5,"error":{"code":-32603,"message":"boom"}}`},
		{"s", `{"jsonrpc":"2.0","id":"r","method":"sampling/createMessage","params":{}}`},
		{"c", `{"jsonrpc":"2.0","id":"r","result":{}}`},
		{"c", `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"slow"}}`},
		{"c", `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":6}}`},
		{"c", "not json"},
	}
	run(NewTracker(Options{Transport: "tcp", CapturePayloads: true}), server...)
	run(NewTracker(Options{Transport: "tcp", Role: "client", ServerAddress: "h", ServerPort: 443}), server...)

	names := map[string]bool{}
	for _, s := range spans.GetSpans() {
		for _, kv := range s.Attributes {
			names[string(kv.Key)] = true
		}
	}
	var rm metricdata.ResourceMetrics
	reader.Collect(context.Background(), &rm)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names[m.Name] = true
			switch d := m.Data.(type) {
			case metricdata.Histogram[float64]:
				for _, dp := range d.DataPoints {
					for _, kv := range dp.Attributes.ToSlice() {
						names[string(kv.Key)] = true
					}
				}
			case metricdata.Histogram[int64]:
				for _, dp := range d.DataPoints {
					for _, kv := range dp.Attributes.ToSlice() {
						names[string(kv.Key)] = true
					}
				}
			case metricdata.Sum[int64]:
				for _, dp := range d.DataPoints {
					for _, kv := range dp.Attributes.ToSlice() {
						names[string(kv.Key)] = true
					}
				}
			}
		}
	}
	return names
}

// TestEmittedNamesAreInSemconv fails when we emit a name the pinned semantic
// conventions don't define (and it isn't declared project-defined), and when
// a project-defined name has since been adopted upstream.
func TestEmittedNamesAreInSemconv(t *testing.T) {
	registry, emitted := registryNames(t), emittedNames(t)
	var unknown, adopted []string
	for n := range emitted {
		if !registry[n] && !projectDefined[n] {
			unknown = append(unknown, n)
		}
	}
	for n := range projectDefined {
		if registry[n] {
			adopted = append(adopted, n)
		}
	}
	var stale []string
	for n := range projectDefined {
		if !emitted[n] {
			stale = append(stale, n)
		}
	}
	sort.Strings(unknown)
	sort.Strings(adopted)
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("projectDefined lists names the scenario never emits (remove them, or extend the scenario):\n  %s", strings.Join(stale, "\n  "))
	}
	if len(unknown) > 0 {
		t.Errorf("emitted names not in the semantic conventions (rename to the semconv name, or add to projectDefined and mark † in the wiki):\n  %s", strings.Join(unknown, "\n  "))
	}
	if len(adopted) > 0 {
		t.Errorf("project-defined names now exist in the semantic conventions: check the definition matches, then drop them from projectDefined and the wiki's †:\n  %s", strings.Join(adopted, "\n  "))
	}
	if len(emitted) < 20 {
		t.Fatalf("only %d names emitted: the scenario no longer exercises the Tracker", len(emitted))
	}
	t.Logf("%d emitted names checked against %d registry names", len(emitted), len(registry))
}
