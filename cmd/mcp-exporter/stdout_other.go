//go:build !unix

package main

import (
	"fmt"
	"os"
	"strings"
)

// reserveStdout can't swap the stdout handle under already-open files here
// (console exporters capture os.Stdout at init), so it refuses the one
// configuration that would write telemetry into the protocol stream.
func reserveStdout() (*os.File, error) {
	for _, k := range []string{"OTEL_TRACES_EXPORTER", "OTEL_METRICS_EXPORTER", "OTEL_LOGS_EXPORTER"} {
		if strings.Contains(os.Getenv(k), "console") {
			return nil, fmt.Errorf("%s=console would write telemetry into the stdio protocol stream on this platform; use otlp or none", k)
		}
	}
	return os.Stdout, nil
}
