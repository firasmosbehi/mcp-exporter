// Package proxy carries MCP traffic between client and server and feeds every
// frame to an observe.Tracker.
package proxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"

	"github.com/firasmosbehi/mcp-exporter/internal/observe"
)

// Stdio runs argv as a child MCP server, relaying newline-delimited JSON-RPC
// between stdin/stdout and the child's, and returns the child's exit code.
func Stdio(ctx context.Context, t *observe.Tracker, argv []string, stdin io.Reader, stdout io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = os.Environ()
	in, err := cmd.StdinPipe()
	if err != nil {
		return 1, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return 1, err
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return 1, err
	}
	if err := cmd.Start(); err != nil {
		return 1, err
	}

	go func() {
		pipeLines(stdin, in, func(l []byte) []byte { return t.Frame(ctx, observe.ClientToServer, "stdio", "", l) })
		in.Close()
	}()
	done := make(chan struct{}, 2)
	go func() {
		pipeLines(out, stdout, func(l []byte) []byte { return t.Frame(ctx, observe.ServerToClient, "stdio", "", l) })
		done <- struct{}{}
	}()
	go func() {
		pipeLines(errPipe, os.Stderr, func(l []byte) []byte { t.LogStderr(string(l)); return l })
		done <- struct{}{}
	}()
	<-done // Wait closes the pipes, so drain them first.
	<-done

	err = cmd.Wait()
	t.EndSession("")
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

// pipeLines copies r to w line by line, passing each non-empty line through f.
// bufio.Reader rather than Scanner: MCP lines can exceed Scanner's token limit.
func pipeLines(r io.Reader, w io.Writer, f func([]byte) []byte) {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if body := bytes.TrimRight(line, "\r\n"); len(body) > 0 {
			if _, werr := w.Write(append(f(body), '\n')); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}
