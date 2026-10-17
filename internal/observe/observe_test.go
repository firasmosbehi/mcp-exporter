package observe

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Issue #9: SDK errors become a per-signal counter; stderr logging is rate-limited.
func TestErrorHandler(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	h := newErrorHandler()
	for _, msg := range []string{
		"traces export: context deadline exceeded",
		"failed to upload metrics: connection refused",
		`Post "http://x/v1/logs": dial tcp: connection refused`,
		"something else",
		"traces export: again",
	} {
		h.Handle(errors.New(msg))
	}
	if h.suppressed != 4 {
		t.Errorf("only the first error should be logged within 30s, suppressed = %d", h.suppressed)
	}

	var rm metricdata.ResourceMetrics
	reader.Collect(context.Background(), &rm)
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "mcp_exporter.export.errors" {
				for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
					v, _ := dp.Attributes.Value("signal")
					got[v.AsString()] = dp.Value
				}
			}
		}
	}
	want := map[string]int64{"traces": 2, "metrics": 1, "logs": 1, "other": 1}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("signal %s: got %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
}
