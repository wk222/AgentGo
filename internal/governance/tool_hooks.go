package governance

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/cloudwego/eino/compose"
)

// ToolCall identifies one real tool execution for hooks.
type ToolCall struct {
	CallID    string // the model's tool call id; empty if the caller did not provide one
	Name      string
	Arguments string // the arguments that will actually run (after any approval override)
	SessionID string
	Workspace string
}

// ToolOutcome is what the tool returned.
type ToolOutcome struct {
	Output   string
	Err      error
	Duration time.Duration
}

// ToolHook lets a plugin observe or veto real tool executions.
//
// Semantics, fixed so plugins can rely on them:
//   - Hooks run in registration order, around the actual execution only. A call
//     that is waiting for approval, was rejected, or was denied by policy never
//     reaches them, so Before sees exactly the calls that are about to run.
//   - Before returning an error vetoes the call: the tool does not run, the
//     model receives a "rejected" result naming the hook, and later hooks and
//     After are skipped. A panic in Before also vetoes (fail closed).
//   - After is observational: it cannot change the result, and a panic in it is
//     logged and ignored. It runs for every call that Before allowed, whether
//     the tool succeeded, failed or was cancelled.
//   - A call whose context is already cancelled does not run and is not retried;
//     governance never retries a tool, since tools may not be idempotent.
type ToolHook struct {
	Name   string // plugin id, shown in veto messages and audit
	Before func(ctx context.Context, c ToolCall) error
	After  func(ctx context.Context, c ToolCall, o ToolOutcome)
}

type hookEntry struct {
	id   int
	hook ToolHook
}

var (
	hooksMu   sync.RWMutex
	hooks     []hookEntry
	hooksNext int
)

// RegisterToolHook adds h after the existing hooks. The returned func removes
// it, so unloading the plugin lifts the interception.
func RegisterToolHook(h ToolHook) (dispose func()) {
	hooksMu.Lock()
	defer hooksMu.Unlock()
	hooksNext++
	id := hooksNext
	hooks = append(hooks, hookEntry{id: id, hook: h})
	return func() {
		hooksMu.Lock()
		defer hooksMu.Unlock()
		for i, e := range hooks {
			if e.id == id {
				hooks = append(hooks[:i:i], hooks[i+1:]...)
				return
			}
		}
	}
}

func hookSnapshot() []ToolHook {
	hooksMu.RLock()
	defer hooksMu.RUnlock()
	out := make([]ToolHook, len(hooks))
	for i, e := range hooks {
		out[i] = e.hook
	}
	return out
}

type callIDKey struct{}

// WithCallID records the model's tool call id for hooks.
func WithCallID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, callIDKey{}, id)
}

func callIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(callIDKey{}).(string); ok && id != "" {
		return id
	}
	return compose.GetToolCallID(ctx)
}

func (h ToolHook) runBefore(ctx context.Context, c ToolCall) (err error) {
	if h.Before == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("hook panicked: %v", r)
		}
	}()
	return h.Before(ctx, c)
}

func (h ToolHook) runAfter(ctx context.Context, c ToolCall, o ToolOutcome) {
	if h.After == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[governance] tool hook %q After panicked on %s: %v", h.Name, c.Name, r)
		}
	}()
	h.After(ctx, c, o)
}

// withHooks wraps the real execution so every path that runs the tool (direct,
// pre-approved, resumed after approval) goes through the same hooks.
func (m *GovernanceMiddleware) withHooks(toolName string, endpoint ToolInvokeFunc) ToolInvokeFunc {
	return func(ctx context.Context, args string) (string, error) {
		hs := hookSnapshot()
		if len(hs) == 0 {
			return endpoint(ctx, args)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		call := ToolCall{
			CallID: callIDFrom(ctx), Name: toolName, Arguments: args,
			SessionID: SessionIDFromContext(ctx), Workspace: m.policy.WorkspaceRoot,
		}
		for _, h := range hs {
			if err := h.runBefore(ctx, call); err != nil {
				m.recordAudit(ctx, "hook_deny", toolName, args, "rejected", "", fmt.Sprintf("hook %s: %v", h.Name, err))
				return m.buildDisapprovedResponse(toolName, args, fmt.Sprintf("blocked by %s: %v", h.Name, err))
			}
		}
		start := time.Now()
		out, err := endpoint(ctx, args)
		o := ToolOutcome{Output: out, Err: err, Duration: time.Since(start)}
		for _, h := range hs {
			h.runAfter(ctx, call, o)
		}
		return out, err
	}
}
