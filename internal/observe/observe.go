// Package observe wires OpenTelemetry and turns MCP traffic into telemetry.
package observe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Setup installs global trace, metric and log providers. Exporters, endpoints,
// protocols, TLS, headers and sampling all come from the standard OTEL_* env
// vars (OTEL_TRACES_EXPORTER=otlp|console|none, OTEL_METRICS_EXPORTER=
// otlp|prometheus|console|none, ...). The returned func flushes and shuts down.
func Setup(ctx context.Context, serviceName, version string) (func(context.Context) error, error) {
	if os.Getenv("OTEL_SERVICE_NAME") == "" {
		os.Setenv("OTEL_SERVICE_NAME", serviceName)
	}
	// SDK self-metrics (otel.sdk.*: processed/dropped spans and logs, exporter
	// outcomes, queue sizes). Experimental in the Go SDK; opt out with =false.
	if _, set := os.LookupEnv("OTEL_GO_X_OBSERVABILITY"); !set {
		os.Setenv("OTEL_GO_X_OBSERVABILITY", "true")
	}
	distroVersion = version
	p, err := NewProviders(ctx, "")
	if err != nil {
		return nil, err
	}
	otel.SetTracerProvider(p.Tracer)
	otel.SetMeterProvider(p.Meter)
	global.SetLoggerProvider(p.Logger)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	otel.SetErrorHandler(newErrorHandler())
	return p.Shutdown, nil
}

var distroVersion = "dev"

// Providers is one set of trace, metric and log providers sharing a resource.
type Providers struct {
	Tracer *sdktrace.TracerProvider
	Meter  *sdkmetric.MeterProvider
	Logger *sdklog.LoggerProvider
}

// NewProviders builds providers configured from the OTEL_* env vars, with
// service.name overridden when serviceName is set (one per gateway route).
func NewProviders(ctx context.Context, serviceName string) (*Providers, error) {
	attrs := []attribute.KeyValue{
		attribute.String("telemetry.distro.name", "mcp-exporter"),
		attribute.String("telemetry.distro.version", distroVersion),
	}
	if serviceName != "" {
		attrs = append(attrs, attribute.String("service.name", serviceName))
	}
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithAttributes(attrs...), // last, so it wins over the env
	)
	if err != nil {
		return nil, err
	}

	spanExp, err := spanExporter(ctx)
	if err != nil {
		return nil, err
	}
	metricReader, err := metricReader(ctx)
	if err != nil {
		return nil, err
	}
	logExp, err := logExporter(ctx)
	if err != nil {
		return nil, err
	}
	return &Providers{
		Tracer: sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithBatcher(spanExp)),
		Meter:  sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(metricReader)),
		Logger: sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp))),
	}, nil
}

// Shutdown flushes and stops all three providers.
func (p *Providers) Shutdown(ctx context.Context) error {
	return errors.Join(p.Tracer.Shutdown(ctx), p.Meter.Shutdown(ctx), p.Logger.Shutdown(ctx))
}

// errorHandler counts SDK errors (mostly failed exports) by signal, so a dead
// backend shows up in metrics, and rate-limits the stderr logging of them.
type errorHandler struct {
	errors     metric.Int64Counter
	mu         sync.Mutex
	last       time.Time
	suppressed int
}

func newErrorHandler() *errorHandler {
	c, _ := otel.Meter(scopeName).Int64Counter("mcp_exporter.export.errors", metric.WithUnit("{error}"),
		metric.WithDescription("Errors reported by the OpenTelemetry SDK, mostly failed exports."))
	return &errorHandler{errors: c}
}

func (h *errorHandler) Handle(err error) {
	h.errors.Add(context.Background(), 1, metric.WithAttributes(attribute.String("signal", errSignal(err))))
	h.mu.Lock()
	defer h.mu.Unlock()
	if time.Since(h.last) < 30*time.Second {
		h.suppressed++
		return
	}
	slog.Warn("opentelemetry", "err", err, "suppressed_since_last", h.suppressed)
	h.last, h.suppressed = time.Now(), 0
}

// errSignal guesses the signal from the SDK's error text; good enough for a
// label, and "other" when unsure.
func errSignal(err error) string {
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "trace") || strings.Contains(s, "span"):
		return "traces"
	case strings.Contains(s, "metric"):
		return "metrics"
	case strings.Contains(s, "log"):
		return "logs"
	}
	return "other"
}

// Console exporters are built here, writing to stderr, rather than by
// autoexport, which writes to stdout: in stdio mode stdout is the protocol
// channel, and only Unix can swap the descriptor out from under them.
func console(signal string) bool {
	return strings.TrimSpace(os.Getenv("OTEL_"+signal+"_EXPORTER")) == "console"
}

func spanExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	if console("TRACES") {
		return stdouttrace.New(stdouttrace.WithWriter(os.Stderr))
	}
	return autoexport.NewSpanExporter(ctx)
}

func metricReader(ctx context.Context) (sdkmetric.Reader, error) {
	if strings.TrimSpace(os.Getenv("OTEL_METRICS_EXPORTER")) == "prometheus" {
		return promReader()
	}
	if console("METRICS") {
		exp, err := stdoutmetric.New(stdoutmetric.WithWriter(os.Stderr))
		if err != nil {
			return nil, err
		}
		return sdkmetric.NewPeriodicReader(exp), nil // honours OTEL_METRIC_EXPORT_INTERVAL
	}
	return autoexport.NewMetricReader(ctx)
}

func logExporter(ctx context.Context) (sdklog.Exporter, error) {
	if console("LOGS") {
		return stdoutlog.New(stdoutlog.WithWriter(os.Stderr))
	}
	return autoexport.NewLogExporter(ctx)
}

var (
	promOnce sync.Once
	promReg  = prometheus.NewRegistry()
)

// promReader replaces autoexport's Prometheus reader, which starts a server
// per reader: gateway routes each have a reader, and all must share one
// /metrics. service.name becomes a service_name label to keep routes apart.
func promReader() (sdkmetric.Reader, error) {
	promOnce.Do(func() {
		host, port := os.Getenv("OTEL_EXPORTER_PROMETHEUS_HOST"), os.Getenv("OTEL_EXPORTER_PROMETHEUS_PORT")
		if host == "" {
			host = "localhost"
		}
		if port == "" {
			port = "9464"
		}
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.HandlerFor(promReg, promhttp.HandlerOpts{}))
		srv := &http.Server{Addr: net.JoinHostPort(host, port), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := srv.ListenAndServe(); err != nil {
				otel.Handle(fmt.Errorf("prometheus metrics listener: %w", err))
			}
		}()
	})
	return promexporter.New(promexporter.WithRegisterer(promReg),
		promexporter.WithResourceAsConstantLabels(attribute.NewAllowKeysFilter("service.name")))
}
