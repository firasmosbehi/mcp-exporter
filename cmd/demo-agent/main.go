// Command demo-agent is a pretend AI agent for the demo stack. Each step is
// an invoke_agent span with a fake model call and some MCP tool calls; the
// step's trace context goes into params._meta, so the MCP spans recorded by
// mcp-exporter become children of the step.
//
//	demo-agent -url http://mcp-exporter:8080/mcp
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/firasmosbehi/mcp-exporter/internal/observe"
)

const model = "demo-model"

func main() {
	url := flag.String("url", "http://localhost:8080/mcp", "MCP endpoint (Streamable HTTP)")
	interval := flag.Duration("interval", 3*time.Second, "pause between agent steps")
	steps := flag.Int("steps", 0, "stop after this many steps (0: run until stopped)")
	tasks := flag.Bool("tasks", true, "make some tool calls MCP Tasks")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdown, err := observe.Setup(ctx, "demo-agent", "dev")
	if err != nil {
		slog.Error("telemetry setup", "err", err)
		os.Exit(1)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdown(c)
	}()

	a := &agent{mcp: &client{url: *url}, tracer: otel.Tracer("demo-agent"), tasks: *tasks}
	for i := 1; *steps == 0 || i <= *steps; i++ {
		if err := a.step(ctx, i); err != nil {
			slog.Warn("step failed", "step", i, "err", err)
			a.mcp.session = "" // reconnect on the next step
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(*interval):
		}
	}
}

type agent struct {
	mcp    *client
	tracer trace.Tracer
	tasks  bool
}

// step is one agent turn: think, call one to three tools, answer.
func (a *agent) step(ctx context.Context, n int) (err error) {
	ctx, span := a.tracer.Start(ctx, "invoke_agent demo-agent", trace.WithAttributes(
		attribute.String("gen_ai.operation.name", "invoke_agent"),
		attribute.String("gen_ai.agent.name", "demo-agent"),
	))
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(attribute.String("error.type", "_OTHER"))
		}
		span.End()
	}()
	if a.mcp.session == "" {
		if err := a.mcp.initialize(ctx); err != nil {
			return err
		}
	}
	a.chat(ctx, 400)
	for range 1 + rand.IntN(3) {
		var err error
		switch r := rand.IntN(10); {
		case a.tasks && n%5 == 0 && r < 3:
			err = a.task(ctx)
		case r < 6:
			_, err = a.mcp.call(ctx, "tools/call", map[string]any{"name": "get-sum",
				"arguments": map[string]any{"a": rand.IntN(100), "b": rand.IntN(100)}})
		default:
			_, err = a.mcp.call(ctx, "tools/call", map[string]any{"name": "get-structured-content",
				"arguments": map[string]any{"location": "Chicago"}})
		}
		if err != nil {
			return err
		}
	}
	a.chat(ctx, 900)
	return nil
}

// chat records a pretend model call with GenAI attributes.
func (a *agent) chat(ctx context.Context, inputTokens int) {
	_, span := a.tracer.Start(ctx, "chat "+model, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.provider.name", "demo"),
		attribute.String("gen_ai.request.model", model),
		attribute.String("gen_ai.response.model", model),
	))
	time.Sleep(time.Duration(50+rand.IntN(250)) * time.Millisecond)
	span.SetAttributes(
		attribute.Int("gen_ai.usage.input_tokens", inputTokens+rand.IntN(200)),
		attribute.Int("gen_ai.usage.output_tokens", 20+rand.IntN(200)),
	)
	span.End()
}

// task runs a tool as an MCP Task: create, poll until terminal, fetch result.
func (a *agent) task(ctx context.Context) error {
	res, err := a.mcp.call(ctx, "tools/call", map[string]any{"name": "simulate-research-query",
		"arguments": map[string]any{"topic": "observability"}, "task": map[string]any{"ttl": 60000}})
	if err != nil {
		return err
	}
	var created struct {
		Task struct{ TaskID string } `json:"task"`
	}
	json.Unmarshal(res, &created)
	if created.Task.TaskID == "" {
		return errors.New("no task created")
	}
	ref := map[string]any{"taskId": created.Task.TaskID}
	for range 30 {
		time.Sleep(time.Second)
		res, err := a.mcp.call(ctx, "tasks/get", ref)
		if err != nil {
			return err
		}
		var st struct{ Status string }
		json.Unmarshal(res, &st)
		if st.Status == "completed" || st.Status == "failed" || st.Status == "cancelled" {
			break
		}
	}
	_, err = a.mcp.call(ctx, "tasks/result", ref)
	return err
}

// client is a minimal MCP Streamable HTTP client.
type client struct {
	url, session string
	id           int
}

func (c *client) initialize(ctx context.Context) error {
	c.session = ""
	if _, err := c.call(ctx, "initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "demo-agent", "version": "1.0"}}); err != nil {
		return err
	}
	resp, err := c.post(ctx, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if err == nil {
		resp.Body.Close()
	}
	return err
}

// call sends a request with the current trace context in params._meta and
// returns its result.
func (c *client) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	meta := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, meta)
	params["_meta"] = meta
	c.id++
	resp, err := c.post(ctx, map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var msg struct {
		ID     *int            `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	found := false
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(nil, 16<<20)
		for !found && sc.Scan() {
			if d, ok := strings.CutPrefix(sc.Text(), "data:"); ok {
				msg.ID = nil
				found = json.Unmarshal([]byte(d), &msg) == nil && msg.ID != nil && *msg.ID == c.id
			}
		}
	} else {
		found = json.NewDecoder(resp.Body).Decode(&msg) == nil
	}
	switch {
	case !found:
		return nil, fmt.Errorf("%s: no response", method)
	case msg.Error != nil:
		return nil, fmt.Errorf("%s: %d %s", method, msg.Error.Code, msg.Error.Message)
	}
	return msg.Result, nil
}

func (c *client) post(ctx context.Context, body any) (*http.Response, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		c.session = s
	}
	return resp, nil
}
