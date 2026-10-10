package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/governance"
)

// WatchRequest asks the host to arm a durable event trigger that wakes THIS session when it fires.
type WatchRequest struct {
	Kind         string `json:"kind"`
	PID          int    `json:"pid,omitempty"`
	Path         string `json:"path,omitempty"`
	Pattern      string `json:"pattern,omitempty"`
	Prompt       string `json:"prompt,omitempty"`
	Title        string `json:"title,omitempty"`
	SessionID    string `json:"session_id"`
	ExpiresInSec int64  `json:"expires_in_sec,omitempty"`
}

// WatchHandler is implemented by the host (bridge) that owns the trigger engine.
type WatchHandler interface {
	CreateWatch(ctx context.Context, req WatchRequest) (any, error)
	ListWatches(ctx context.Context, sessionID string) (any, error)
	CancelWatch(ctx context.Context, sessionID, id string) error
}

var (
	watchMu      sync.RWMutex
	watchHandler WatchHandler
)

// SetWatchHandler wires the trigger engine into the watch_event tool.
func SetWatchHandler(h WatchHandler) {
	watchMu.Lock()
	watchHandler = h
	watchMu.Unlock()
}

type watchEventInput struct {
	Action      string  `json:"action" jsonschema:"description=Action: 'create', 'list' or 'cancel'"`
	Kind        string  `json:"kind,omitempty" jsonschema:"description=What to wait for: 'file' (a marker file appears)\\, 'log_match' (a NEW log line matches pattern)\\, 'process_exit' (a process ends)\\, or 'webhook' (an external HTTP call)"`
	Path        string  `json:"path,omitempty" jsonschema:"description=Absolute path of the marker file or log file (kind=file or log_match)"`
	Pattern     string  `json:"pattern,omitempty" jsonschema:"description=Regular expression for kind=log_match\\, e.g. 'DONE|Traceback'"`
	PID         int     `json:"pid,omitempty" jsonschema:"description=Process id to wait for (kind=process_exit)"`
	Prompt      string  `json:"prompt,omitempty" jsonschema:"description=What you (the agent) should do when woken\\, e.g. 'analyse results.csv and report'"`
	Title       string  `json:"title,omitempty" jsonschema:"description=Short label shown in the trigger list"`
	TimeoutHour float64 `json:"timeout_hours,omitempty" jsonschema:"description=Give up after this many hours (default 24\\, max 168)"`
	ID          string  `json:"id,omitempty" jsonschema:"description=Trigger id for cancel"`
}

type watchEventOutput struct {
	Success bool   `json:"success"`
	Action  string `json:"action"`
	Result  any    `json:"result,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

const (
	defaultWatchHours = 24.0
	maxWatchHours     = 168.0
)

// RegisterWatchEventTool registers `watch_event`: after launching a long job the agent can end its
// turn (burning no tokens) and be woken in the same session when the event happens. Unlike
// job_monitor it survives app restarts and also covers jobs started elsewhere (e.g. on a server).
func RegisterWatchEventTool(r *Registry) error {
	t, err := utils.InferTool("watch_event",
		"Wait for an external event WITHOUT spending tokens, then be woken up in this same conversation. "+
			"Use after starting a long experiment: arm a trigger (marker file appears / new log line matches / process exits / webhook), then finish your turn. "+
			"Prefer a marker file or process_exit for 'job finished'. The wake-up message carries the event details and your prompt.",
		func(ctx context.Context, in watchEventInput) (watchEventOutput, error) {
			watchMu.RLock()
			h := watchHandler
			watchMu.RUnlock()
			if h == nil {
				return watchEventOutput{Action: in.Action, Error: "event triggers are not available in this host"}, nil
			}
			sid := governance.SessionIDFromContext(ctx)

			switch strings.ToLower(strings.TrimSpace(in.Action)) {
			case "create":
				hours := in.TimeoutHour
				if hours <= 0 {
					hours = defaultWatchHours
				}
				if hours > maxWatchHours {
					hours = maxWatchHours
				}
				res, err := h.CreateWatch(ctx, WatchRequest{
					Kind: strings.TrimSpace(in.Kind), PID: in.PID, Path: strings.TrimSpace(in.Path),
					Pattern: in.Pattern, Prompt: in.Prompt, Title: in.Title,
					SessionID: sid, ExpiresInSec: int64(hours * 3600),
				})
				if err != nil {
					return watchEventOutput{Action: "create", Error: err.Error()}, nil
				}
				return watchEventOutput{Success: true, Action: "create", Result: res,
					Message: "触发器已登记。现在可以结束本轮对话；事件发生时会在本会话中唤醒你。"}, nil
			case "list":
				res, err := h.ListWatches(ctx, sid)
				if err != nil {
					return watchEventOutput{Action: "list", Error: err.Error()}, nil
				}
				return watchEventOutput{Success: true, Action: "list", Result: res}, nil
			case "cancel":
				if strings.TrimSpace(in.ID) == "" {
					return watchEventOutput{Action: "cancel", Error: "id is required"}, nil
				}
				if err := h.CancelWatch(ctx, sid, strings.TrimSpace(in.ID)); err != nil {
					return watchEventOutput{Action: "cancel", Error: err.Error()}, nil
				}
				return watchEventOutput{Success: true, Action: "cancel", Message: "已取消"}, nil
			default:
				return watchEventOutput{Action: in.Action, Error: fmt.Sprintf("unknown action %q, use create/list/cancel", in.Action)}, nil
			}
		},
	)
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}
