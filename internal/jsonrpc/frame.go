// Package jsonrpc parses the JSON-RPC 2.0 messages MCP is built on.
package jsonrpc

import (
	"bytes"
	"encoding/json"
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
		return json.Marshal(msgs)
	}
	return json.Marshal(msgs[0])
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
	Meta            map[string]string `json:"-"`
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
}

func (m *Message) ParseResult() Result {
	var r Result
	_ = json.Unmarshal(m.Result, &r)
	return r
}

// SetMeta merges kv into params._meta, creating params if needed.
// Params that aren't a JSON object are left untouched.
func (m *Message) SetMeta(kv map[string]string) {
	params := map[string]json.RawMessage{}
	if len(m.Params) > 0 && json.Unmarshal(m.Params, &params) != nil {
		return
	}
	meta := map[string]json.RawMessage{}
	if raw, ok := params["_meta"]; ok && json.Unmarshal(raw, &meta) != nil {
		return
	}
	for k, v := range kv {
		b, _ := json.Marshal(v)
		meta[k] = b
	}
	params["_meta"], _ = json.Marshal(meta)
	m.Params, _ = json.Marshal(params)
}
