package acpclient

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Permission is what an agent may do without a person saying yes. The agent asks before it acts
// (session/request_permission); the level decides the answer. A host that does not trust the
// agent starts at ReadOnly.
type Permission int

const (
	ReadOnly Permission = iota // read, search, fetch, think
	Edit                       // …and change, move or delete files
	Full                       // …and run commands and anything else it can ask for
)

func (p Permission) String() string {
	switch p {
	case Edit:
		return "edit"
	case Full:
		return "full"
	}
	return "read_only"
}

// ParsePermission accepts the names the String method produces, plus a few spellings of them.
func ParsePermission(s string) (Permission, error) {
	switch strings.ToLower(strings.TrimSpace(strings.ReplaceAll(s, "-", "_"))) {
	case "", "read_only", "readonly", "read":
		return ReadOnly, nil
	case "edit", "write":
		return Edit, nil
	case "full", "all", "yolo":
		return Full, nil
	}
	return ReadOnly, fmt.Errorf("unknown permission %q (use read_only, edit or full)", s)
}

// allows applies the levels to ACP's tool kinds.
func (p Permission) allows(kind string) bool {
	switch kind {
	case "read", "search", "fetch", "think":
		return true
	case "edit", "delete", "move":
		return p >= Edit
	}
	return p >= Full // execute, switch_mode, other, or a kind we do not know
}

type permOption struct {
	ID   string `json:"optionId"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type permRequest struct {
	ToolCall struct {
		ID    string `json:"toolCallId"`
		Title string `json:"title"`
		Kind  string `json:"kind"`
	} `json:"toolCall"`
	Options []permOption `json:"options"`
}

// pick returns the id of the first option whose kind is one of kinds, trying them in order.
func pick(opts []permOption, kinds ...string) (string, bool) {
	for _, k := range kinds {
		for _, o := range opts {
			if o.Kind == k {
				return o.ID, true
			}
		}
	}
	return "", false
}

func (c *Conn) handleRequest(method string, params json.RawMessage) (any, *RPCError) {
	if method != "session/request_permission" {
		// fs/* and terminal/* are not offered in initialize, so a well-behaved agent never asks.
		return nil, &RPCError{Code: -32601, Message: "method not supported by this client: " + method}
	}
	var req permRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &RPCError{Code: -32602, Message: "bad permission request"}
	}
	cancelled := map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
	selected := func(id string) any {
		return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": id}}
	}
	if c.cfg.Permission.allows(req.ToolCall.Kind) {
		if id, ok := pick(req.Options, "allow_once", "allow_always"); ok {
			return selected(id), nil
		}
		return cancelled, nil
	}
	c.mu.Lock()
	if c.turn != nil {
		what := strings.TrimSpace(req.ToolCall.Title)
		if what == "" {
			what = req.ToolCall.Kind
		}
		c.turn.denied = append(c.turn.denied, fmt.Sprintf("%s (%s)", what, req.ToolCall.Kind))
	}
	c.mu.Unlock()
	if id, ok := pick(req.Options, "reject_once", "reject_always"); ok {
		return selected(id), nil
	}
	return cancelled, nil
}

type updateParams struct {
	Update struct {
		Kind    string `json:"sessionUpdate"`
		Content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		ToolCallID string `json:"toolCallId"`
		Title      string `json:"title"`
		ToolKind   string `json:"kind"`
		Status     string `json:"status"`
	} `json:"update"`
}

func (c *Conn) handleNotify(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	var p updateParams
	if json.Unmarshal(params, &p) != nil {
		return
	}
	u := p.Update
	var ev *Event
	c.mu.Lock()
	t := c.turn
	if t != nil {
		switch u.Kind {
		case "agent_message_chunk":
			if u.Content.Type == "text" || u.Content.Type == "" {
				t.text.WriteString(u.Content.Text)
				ev = &Event{Kind: "text", Text: u.Content.Text}
			}
		case "tool_call", "tool_call_update":
			call := t.upsert(u.ToolCallID, u.Title, u.ToolKind, u.Status)
			ev = &Event{Kind: "tool", Tool: call}
		}
	}
	emit := (*turnState)(nil)
	if t != nil {
		emit = t
	}
	c.mu.Unlock()
	if ev != nil && emit != nil && emit.emit != nil {
		emit.emit(*ev)
	}
}

// upsert records a tool call or folds an update into the call it belongs to. A field the update
// leaves empty keeps its earlier value.
func (t *turnState) upsert(id, title, kind, status string) ToolCall {
	for i := range t.tools {
		if t.tools[i].ID == id && id != "" {
			if title != "" {
				t.tools[i].Title = title
			}
			if kind != "" {
				t.tools[i].Kind = kind
			}
			if status != "" {
				t.tools[i].Status = status
			}
			return t.tools[i]
		}
	}
	call := ToolCall{ID: id, Title: title, Kind: kind, Status: status}
	t.tools = append(t.tools, call)
	return call
}
