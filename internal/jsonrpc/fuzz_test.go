package jsonrpc

import (
	"bytes"
	"encoding/json"
	"testing"
)

var seeds = []string{
	`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
	`{"jsonrpc":"2.0","id":"a","method":"tools/call","params":{"name":"x","arguments":{"k":[1,2]},"_meta":{"progressToken":3,"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}}}`,
	`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	`{"jsonrpc":"2.0","id":1,"result":{"task":{"taskId":"t","status":"working"},"_meta":{"io.modelcontextprotocol/related-task":{"taskId":"t"}}}}`,
	`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"nope"}}`,
	`[{"jsonrpc":"2.0","id":1,"method":"a"},{"jsonrpc":"2.0","id":2,"method":"b","params":[1]}]`,
	`{"jsonrpc":"2.0","id":null,"method":"x","params":{"_meta":null}}`,
	`[]`, `{}`, `null`, `"str"`, `{"id":1e999}`,
	`{"jsonrpc":"2.0","id":{"": 0},"method":"x"}`, // found in CI: whitespace in a (non-standard) object id
}

// compact strips insignificant whitespace; re-encoding may do the same.
func compact(b []byte) string {
	var out bytes.Buffer
	if json.Compact(&out, b) != nil {
		return string(b)
	}
	return out.String()
}

// Parsing untrusted frames must never panic, and adding trace context must
// produce a frame that re-parses to the same messages.
func FuzzParse(f *testing.F) {
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		msgs, batch, err := Parse(b)
		if err != nil {
			return
		}
		for i := range msgs {
			msgs[i].ParseParams()
			msgs[i].ParseResult()
			msgs[i].IsRequest()
			msgs[i].SetMeta(map[string]string{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"})
		}
		if batch && len(msgs) == 0 {
			return // "[]" re-encodes fine but has nothing to compare
		}
		out, err := Encode(msgs, batch)
		if err != nil {
			t.Fatalf("Encode after SetMeta failed: %v", err)
		}
		again, batch2, err := Parse(out)
		if err != nil || batch2 != batch || len(again) != len(msgs) {
			t.Fatalf("re-parse of %s: err=%v batch=%v/%v len=%d/%d", out, err, batch2, batch, len(again), len(msgs))
		}
		for i := range msgs {
			// encoding/json compacts raw values, so ids are equal modulo whitespace.
			if compact(again[i].ID) != compact(msgs[i].ID) || again[i].Method != msgs[i].Method {
				t.Fatalf("message %d changed: id %s->%s method %q->%q", i, msgs[i].ID, again[i].ID, msgs[i].Method, again[i].Method)
			}
		}
	})
}
