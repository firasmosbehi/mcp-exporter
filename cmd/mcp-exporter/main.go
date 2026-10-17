// Command mcp-exporter is a transparent proxy that exports OpenTelemetry
// traces, metrics and logs for any MCP server.
//
//	mcp-exporter stdio [flags] -- <server command> [args...]
//	mcp-exporter http  [flags] -upstream http://server:3000
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/firasmosbehi/mcp-exporter/internal/observe"
	"github.com/firasmosbehi/mcp-exporter/internal/proxy"
)

const usage = `usage:
  mcp-exporter stdio [flags] -- <server command> [args...]
  mcp-exporter http  [flags] -upstream <url>

Telemetry export is configured with the standard OTEL_* environment variables,
e.g. OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_TRACES_EXPORTER=otlp|console|none,
OTEL_METRICS_EXPORTER=otlp|prometheus|none, OTEL_TRACES_SAMPLER.
Run "mcp-exporter <mode> -h" for flags.
`

func main() {
	// stdout is the protocol channel in stdio mode, so our own logs go to stderr.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	code, err := run(os.Args[1], os.Args[2:])
	if err != nil {
		slog.Error("mcp-exporter", "err", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func run(mode string, args []string) (int, error) {
	fs := flag.NewFlagSet(mode, flag.ExitOnError)
	service := fs.String("service-name", "mcp-server", "service.name, unless OTEL_SERVICE_NAME is set")
	inject := fs.Bool("inject-context", true, "add W3C trace context to params._meta of client requests that lack it")
	capture := fs.Bool("capture-payloads", false, "record redacted tool arguments and results on spans")
	maxPayload := fs.Int("max-payload", 4096, "truncate captured payloads to this many bytes")

	var (
		listen, upstream, certFile, keyFile, clientCA *string
		idle                                          *time.Duration
	)
	transport := "pipe"
	switch mode {
	case "stdio":
	case "http":
		transport = "tcp"
		listen = fs.String("listen", ":8080", "address to listen on")
		upstream = fs.String("upstream", "", "base URL of the MCP server (required)")
		idle = fs.Duration("session-idle", 30*time.Minute, "end sessions and requests idle this long")
		certFile = fs.String("tls-cert", "", "serve TLS with this certificate")
		keyFile = fs.String("tls-key", "", "private key for -tls-cert")
		clientCA = fs.String("tls-client-ca", "", "require client certificates signed by this CA (mTLS)")
	default:
		fmt.Fprint(os.Stderr, usage)
		return 2, nil
	}
	fs.Parse(args)

	protocolOut := os.Stdout
	if mode == "stdio" {
		var err error
		if protocolOut, err = reserveStdout(); err != nil {
			return 1, fmt.Errorf("reserving stdout: %w", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdown, err := observe.Setup(ctx, *service)
	if err != nil {
		return 1, fmt.Errorf("telemetry setup: %w", err)
	}
	t := observe.NewTracker(observe.Options{
		Transport: transport, InjectContext: *inject, CapturePayloads: *capture, MaxPayload: *maxPayload,
	})
	defer func() {
		t.Close()
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(flushCtx); err != nil {
			slog.Warn("telemetry flush", "err", err)
		}
	}()

	if mode == "stdio" {
		if fs.NArg() == 0 {
			return 2, errors.New("stdio: missing server command after --")
		}
		return proxy.Stdio(ctx, t, fs.Args(), os.Stdin, protocolOut)
	}

	if *upstream == "" {
		return 2, errors.New("http: -upstream is required")
	}
	up, err := url.Parse(*upstream)
	if err != nil || up.Host == "" {
		return 2, fmt.Errorf("http: bad -upstream %q", *upstream)
	}
	srv := &http.Server{Addr: *listen, Handler: proxy.HTTP(t, up), ReadHeaderTimeout: 10 * time.Second}
	if *clientCA != "" {
		pem, err := os.ReadFile(*clientCA)
		if err != nil {
			return 1, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return 1, fmt.Errorf("no certificates in %s", *clientCA)
		}
		srv.TLSConfig = &tls.Config{ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert}
	}

	go func() {
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				t.Sweep(*idle)
			}
		}
	}()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()

	slog.Info("proxying", "listen", *listen, "upstream", up.String(), "tls", *certFile != "")
	if *certFile != "" {
		err = srv.ListenAndServeTLS(*certFile, *keyFile)
	} else if *clientCA != "" {
		return 2, errors.New("http: -tls-client-ca requires -tls-cert and -tls-key")
	} else {
		err = srv.ListenAndServe()
	}
	if errors.Is(err, http.ErrServerClosed) {
		return 0, nil
	}
	return 1, err
}
