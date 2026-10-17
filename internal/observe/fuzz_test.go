package observe

import (
	"bytes"
	"context"
	"testing"

	"github.com/firasmosbehi/mcp-exporter/internal/jsonrpc"
)

// Frame sees every byte of MCP traffic. It must never panic, must pass
// unparseable frames through untouched, and must emit valid JSON-RPC.
func FuzzFrame(f *testing.F) {
	for _, s := range []string{
		`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"c"}}}`,
		`{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":"2025-11-25","serverInfo":{"name":"s"}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"t","arguments":{"api_key":"sk-abcdefghijklmnopqrstuv"},"task":{}}}`,
		`{"jsonrpc":"2.0","id":1,"result":{"task":{"taskId":"T","status":"working"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/tasks/status","params":{"taskId":"T","status":"completed"}}`,
		`{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"error","data":{"token":"x"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`,
		`[{"jsonrpc":"2.0","id":2,"method":"tasks/get","params":{"taskId":"T"}},{"jsonrpc":"2.0","id":2,"error":{"code":-32602,"message":""}}]`,
		`not json`,
	} {
		f.Add([]byte(s), false)
		f.Add([]byte(s), true)
	}
	tr := NewTracker(Options{Transport: "pipe", InjectContext: true, CapturePayloads: true, MaxPayload: 64})
	f.Fuzz(func(t *testing.T, frame []byte, fromServer bool) {
		dir := ClientToServer
		if fromServer {
			dir = ServerToClient
		}
		in := append([]byte(nil), frame...)
		out := tr.Frame(context.Background(), dir, "s", "", frame)
		if _, _, err := jsonrpc.Parse(in); err != nil {
			if !bytes.Equal(out, in) {
				t.Fatalf("unparseable frame was modified: %q -> %q", in, out)
			}
			return
		}
		if _, _, err := jsonrpc.Parse(out); err != nil {
			t.Fatalf("emitted invalid JSON-RPC %q for input %q: %v", out, in, err)
		}
	})
}
