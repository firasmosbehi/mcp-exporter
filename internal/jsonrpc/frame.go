// Package jsonrpc parses the JSON-RPC 2.0 messages MCP is built on.
package jsonrpc

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
)

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Message is any JSON-RPC message: request, notification or response.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

func (m *Message) hasID() bool { return len(m.ID) > 0 && string(m.ID) != "null" }

func (m *Message) IsRequest() bool      { return m.Method != "" && m.hasID() }
func (m *Message) IsNotification() bool { return m.Method != "" && !m.hasID() }
func (m *Message) IsResponse() bool     { return m.Method == "" && m.hasID() }

// Parse decodes a single message or a batch.
func Parse(b []byte) (msgs []Message, batch bool, err error) {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '[' {
		err = json.Unmarshal(b, &msgs)
		return msgs, true, err
	}
	var m Message
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, false, err
	}
	return []Message{m}, false, nil
}

// Encode is the inverse of Parse.
func Encode(msgs []Message, batch bool) ([]byte, error) {
	if batch {
		return marshal(msgs)
	}
	return marshal(msgs[0])
}

// marshal is json.Marshal without HTML escaping: the proxy should not turn
// "&", "<" or ">" in ids and arguments into \u0026-style escapes.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// Params holds the fields of request params that telemetry cares about.
type Params struct {
	Name            string            `json:"name"` // tools/call, prompts/get
	URI             string            `json:"uri"`  // resources/*
	ProtocolVersion string            `json:"protocolVersion"`
	ClientInfo      *Info             `json:"clientInfo"`
	RequestID       json.RawMessage   `json:"requestId"` // notifications/cancelled
	Level           string            `json:"level"`     // notifications/message
	Logger          string            `json:"logger"`
	Data            json.RawMessage   `json:"data"`
	Arguments       json.RawMessage   `json:"arguments"`
	Task            json.RawMessage   `json:"task"`   // present on task-augmented requests
	TaskID          string            `json:"taskId"` // tasks/get, tasks/result, tasks/cancel, notifications/tasks/status
	Status          string            `json:"status"` // notifications/tasks/status
	Meta            map[string]string `json:"-"`      // string-valued _meta entries (trace context)
	RelatedTask     string            `json:"-"`      // _meta["io.modelcontextprotocol/related-task"].taskId
}

const relatedTaskKey = "io.modelcontextprotocol/related-task"

func relatedTask(meta map[string]any) string {
	if rt, ok := meta[relatedTaskKey].(map[string]any); ok {
		id, _ := rt["taskId"].(string)
		return id
	}
	return ""
}

type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ParseParams extracts Params; unknown or malformed params yield a zero value.
func (m *Message) ParseParams() Params {
	var p Params
	if len(m.Params) == 0 {
		return p
	}
	_ = json.Unmarshal(m.Params, &p)
	var raw struct {
		Meta map[string]any `json:"_meta"`
	}
	_ = json.Unmarshal(m.Params, &raw)
	p.RelatedTask = relatedTask(raw.Meta)
	for k, v := range raw.Meta {
		if s, ok := v.(string); ok {
			if p.Meta == nil {
				p.Meta = map[string]string{}
			}
			p.Meta[k] = s
		}
	}
	return p
}

// Result holds the fields of a response result that telemetry cares about.
type Result struct {
	IsError         bool   `json:"isError"`
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      *Info  `json:"serverInfo"`
	Task            *Task  `json:"task"`   // CreateTaskResult
	TaskID          string `json:"taskId"` // tasks/get, tasks/cancel
	Status          string `json:"status"`
	RelatedTask     string `json:"-"` // _meta related-task, e.g. on tasks/result
}

type Task struct {
	TaskID string `json:"taskId"`
	Status string `json:"status"`
}

func (m *Message) ParseResult() Result {
	var r Result
	_ = json.Unmarshal(m.Result, &r)
	var raw struct {
		Meta map[string]any `json:"_meta"`
	}
	_ = json.Unmarshal(m.Result, &raw)
	r.RelatedTask = relatedTask(raw.Meta)
	return r
}

// SetMeta merges kv into params._meta, creating params if needed.
// Params that aren't a JSON object (arrays, null) are left untouched.
func (m *Message) SetMeta(kv map[string]string) {
	params := map[string]json.RawMessage{}
	// Unmarshalling JSON null into a map yields a nil map, so check for nil too.
	if len(m.Params) > 0 && (json.Unmarshal(m.Params, &params) != nil || params == nil) {
		return
	}
	meta := map[string]json.RawMessage{}
	if raw, ok := params["_meta"]; ok && json.Unmarshal(raw, &meta) != nil {
		return
	}
	if meta == nil { // "_meta": null
		meta = map[string]json.RawMessage{}
	}
	for k, v := range kv {
		b, _ := marshal(v)
		meta[k] = b
	}
	params["_meta"], _ = marshal(meta)
	m.Params, _ = marshal(params)
}

// IDKey returns a canonical form of a JSON-RPC id for matching responses to
// requests. Ids that are the same value written differently match (1, 1.0
// and 1e0; "a" and "\u0061"), while the string "1" and the number 1 stay
// distinct, as JSON-RPC requires. The key is internal: forwarded bytes are
// never changed.
func IDKey(id json.RawMessage) string {
	b := bytes.TrimSpace(id)
	if len(b) == 0 {
		return ""
	}
	switch c := b[0]; {
	case c == '"':
		// Decode only when there are escapes: raw bytes are exact (decoding
		// would map every invalid UTF-8 byte to U+FFFD and merge distinct ids).
		if len(b) >= 2 && b[len(b)-1] == '"' && bytes.IndexByte(b[1:len(b)-1], '\\') < 0 {
			return "s:" + string(b[1:len(b)-1])
		}
		var s string
		if json.Unmarshal(b, &s) == nil {
			return "s:" + s
		}
	case c == '-' || (c >= '0' && c <= '9'):
		if n, ok := numberKey(string(b)); ok {
			return "n:" + n
		}
	}
	var out bytes.Buffer // anything else (null, objects, arrays) by its compacted text
	if json.Compact(&out, b) == nil {
		return "r:" + out.String()
	}
	return "r:" + string(b)
}

// numberKey canonicalises a JSON number. Integer literals stay exact at any
// size (math/big); others go through float64, and if integral within 2^53 they
// are written as integers so 1.0 and 1e0 match 1. Exponents are never expanded
// exactly: "1e999999999" must not allocate a billion digits.
func numberKey(s string) (string, bool) {
	if !strings.ContainsAny(s, ".eE") {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil { // common case, no big.Int
			return strconv.FormatInt(i, 10), true
		}
		var n big.Int
		if _, ok := n.SetString(s, 10); ok {
			return n.String(), true
		}
		return "", false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return "", false
	}
	if f == math.Trunc(f) && math.Abs(f) <= 1<<53 {
		return strconv.FormatFloat(f, 'f', -1, 64), true
	}
	return strconv.FormatFloat(f, 'g', -1, 64), true
}

// IDString is the id as it should appear in telemetry: the string's value or
// the number's canonical text.
func IDString(id json.RawMessage) string {
	k := IDKey(id)
	if len(k) > 2 {
		return k[2:]
	}
	return k
}
