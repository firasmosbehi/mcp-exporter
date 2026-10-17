package jsonrpc

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		in                     string
		n                      int
		batch, req, note, resp bool
	}{
		{`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, 1, false, true, false, false},
		{`{"jsonrpc":"2.0","id":"a","method":"tools/call","params":{"name":"x"}}`, 1, false, true, false, false},
		{`{"jsonrpc":"2.0","method":"notifications/initialized"}`, 1, false, false, true, false},
		{`{"jsonrpc":"2.0","id":1,"result":{}}`, 1, false, false, false, true},
		{`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"nope"}}`, 1, false, false, false, true},
		{` [{"jsonrpc":"2.0","id":1,"method":"a"},{"jsonrpc":"2.0","id":2,"method":"b"}]`, 2, true, true, false, false},
	} {
		msgs, batch, err := Parse([]byte(tc.in))
		if err != nil || len(msgs) != tc.n || batch != tc.batch {
			t.Fatalf("%s: got %d msgs batch=%v err=%v", tc.in, len(msgs), batch, err)
		}
		m := msgs[0]
		if m.IsRequest() != tc.req || m.IsNotification() != tc.note || m.IsResponse() != tc.resp {
			t.Errorf("%s: req=%v note=%v resp=%v", tc.in, m.IsRequest(), m.IsNotification(), m.IsResponse())
		}
	}
	if _, _, err := Parse([]byte("not json")); err == nil {
		t.Error("expected error for garbage")
	}
}

func TestParamsAndMeta(t *testing.T) {
	msgs, _, _ := Parse([]byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"get_weather","arguments":{"city":"Tunis"},"_meta":{"progressToken":3}}}`))
	m := &msgs[0]
	if p := m.ParseParams(); p.Name != "get_weather" || p.Meta["traceparent"] != "" {
		t.Fatalf("params: %+v", p)
	}
	m.SetMeta(map[string]string{"traceparent": "00-abc-def-01"})
	out, _ := Encode(msgs, false)
	for _, want := range []string{`"traceparent":"00-abc-def-01"`, `"progressToken":3`, `"city":"Tunis"`, `"id":7`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("encoded %s lacks %s", out, want)
		}
	}
	if m.ParseParams().Meta["traceparent"] != "00-abc-def-01" {
		t.Error("meta not readable after SetMeta")
	}

	// Request without params gains a params object.
	msgs, _, _ = Parse([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	msgs[0].SetMeta(map[string]string{"traceparent": "x"})
	if msgs[0].ParseParams().Meta["traceparent"] != "x" {
		t.Error("meta not added to param-less request")
	}
}
