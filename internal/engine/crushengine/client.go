package crushengine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a thin HTTP client for a running `crush server` (TCP transport).
type Client struct {
	base     string // e.g. http://127.0.0.1:47831/v1
	clientID string
	hc       *http.Client // short-timeout calls
	stream   *http.Client // no timeout, for SSE
}

func NewClient(addr, clientID string) *Client {
	return &Client{
		base:     "http://" + addr + "/v1",
		clientID: clientID,
		hc:       &http.Client{Timeout: 30 * time.Second},
		stream:   &http.Client{},
	}
}

// StatusError is returned for non-2xx responses.
type StatusError struct {
	Code int
	Msg  string
}

func (e *StatusError) Error() string { return fmt.Sprintf("crush api: HTTP %d: %s", e.Code, e.Msg) }

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode/100 != 2 {
		var ae apiError
		msg := strings.TrimSpace(string(data))
		if json.Unmarshal(data, &ae) == nil && ae.Error != "" {
			msg = ae.Error
		}
		return &StatusError{Code: resp.StatusCode, Msg: msg}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *Client) cidQuery() url.Values { return url.Values{"client_id": {c.clientID}} }

// Health returns nil when the server answers /health.
func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/health", nil, nil, nil)
}

// CreateWorkspace registers (or re-attaches to) a workspace for path.
func (c *Client) CreateWorkspace(ctx context.Context, path string, yolo bool) (workspaceInfo, error) {
	var out workspaceInfo
	err := c.do(ctx, http.MethodPost, "/workspaces", nil, map[string]any{
		"path": path, "client_id": c.clientID, "yolo": yolo,
	}, &out)
	return out, err
}

func (c *Client) DeleteWorkspace(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/workspaces/"+id, c.cidQuery(), nil, nil)
}

func (c *Client) CreateSession(ctx context.Context, wsID, title string) (sessionInfo, error) {
	var out sessionInfo
	err := c.do(ctx, http.MethodPost, "/workspaces/"+wsID+"/sessions", nil, map[string]any{"title": title}, &out)
	return out, err
}

// SendMessage queues a prompt; the turn runs asynchronously (HTTP 202).
func (c *Client) SendMessage(ctx context.Context, wsID, sessionID, runID, prompt string) error {
	return c.do(ctx, http.MethodPost, "/workspaces/"+wsID+"/agent", nil, map[string]any{
		"session_id": sessionID, "run_id": runID, "prompt": prompt,
	}, nil)
}

func (c *Client) CancelSession(ctx context.Context, wsID, sessionID string) error {
	return c.do(ctx, http.MethodPost, "/workspaces/"+wsID+"/agent/sessions/"+sessionID+"/cancel", nil, nil, nil)
}

// GrantPermission resolves a pending permission request.
func (c *Client) GrantPermission(ctx context.Context, wsID string, perm wirePermission, action string) error {
	return c.do(ctx, http.MethodPost, "/workspaces/"+wsID+"/permissions/grant", nil, map[string]any{
		"action": action,
		"permission": map[string]any{
			"id": perm.ID, "session_id": perm.SessionID, "tool_call_id": perm.ToolCallID,
			"tool_name": perm.ToolName, "description": perm.Description,
			"action": perm.Action, "path": perm.Path, "params": perm.Params,
		},
	}, nil)
}

// CancelQuestion dismisses the pending question batch (no UI to answer it).
func (c *Client) CancelQuestion(ctx context.Context, wsID string) error {
	return c.do(ctx, http.MethodPost, "/workspaces/"+wsID+"/questions/cancel", nil, nil, nil)
}

// Stream opens the workspace SSE stream. It returns once response headers are
// received (the subscription is live), then yields envelopes on the channel
// until ctx is cancelled or the stream ends; the channel is then closed and
// the final error (nil on clean EOF/cancel) is readable from errOut.
func (c *Client) Stream(ctx context.Context, wsID string) (<-chan envelope, func() error, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.base+"/workspaces/"+wsID+"/events?"+c.cidQuery().Encode(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.stream.Do(req)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, nil, &StatusError{Code: resp.StatusCode, Msg: strings.TrimSpace(string(data))}
	}
	ch := make(chan envelope, 256)
	var streamErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(ch)
		defer resp.Body.Close()
		streamErr = parseSSE(ctx, resp.Body, ch)
	}()
	return ch, func() error { <-done; return streamErr }, nil
}

// parseSSE reads "data: {json}" records and forwards decoded envelopes.
func parseSSE(ctx context.Context, r io.Reader, out chan<- envelope) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 32<<20) // messages can carry large tool output
	var data []string
	flush := func() bool {
		if len(data) == 0 {
			return true
		}
		raw := strings.Join(data, "\n")
		data = data[:0]
		var env envelope
		if json.Unmarshal([]byte(raw), &env) != nil {
			return true // skip malformed record
		}
		select {
		case out <- env:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if !flush() {
				return nil
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	flush()
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}
