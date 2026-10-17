// Package observe wires OpenTelemetry and turns MCP traffic into telemetry.
package observe

import (
	"context"
	"errors"
	"os"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log/global"
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
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithAttributes(
			attribute.String("telemetry.distro.name", "mcp-exporter"),
			attribute.String("telemetry.distro.version", version),
		),
	)
	if err != nil {
		return nil, err
	}

	spanExp, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, err
	}
	metricReader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, err
	}
	logExp, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithBatcher(spanExp))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(metricReader))
	lp := sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)))

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	global.SetLoggerProvider(lp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	return func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx), lp.Shutdown(ctx))
	}, nil
}
