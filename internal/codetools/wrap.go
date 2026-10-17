//go:build crushparity

package codetools

import (
	"context"

	ct "agentgo/third_party/crush/pkg/codetools"

	"agentgo/internal/coderuntime"
)

// Wrap presents the vendored Crush toolbox as AgentGo's coderuntime.Toolbox.
// It exists only for the parity tests (build tag crushparity): production
// uses the native toolbox (internal/codelsp + Native* tools) exclusively.
func Wrap(tb *ct.Toolbox) coderuntime.Toolbox {
	if tb == nil {
		return nil
	}
	return &crushToolbox{tb: tb}
}

type crushToolbox struct {
	tb *ct.Toolbox
}

func (w *crushToolbox) Tools() []coderuntime.Tool {
	out := make([]coderuntime.Tool, 0, len(w.tb.Tools))
	for _, t := range w.tb.Tools {
		out = append(out, coderuntime.Tool{
			Name: t.Name, Description: t.Description, Schema: t.Schema, Parallel: t.Parallel,
			Run: func(ctx context.Context, sessionID, callID, argsJSON string) (coderuntime.ToolResult, error) {
				res, err := t.Run(ctx, sessionID, callID, argsJSON)
				return coderuntime.ToolResult{Content: res.Content, IsError: res.IsError}, err
			},
		})
	}
	return out
}

func (w *crushToolbox) LSPStates() map[string]coderuntime.LSPClientInfo {
	states := w.tb.LSPStates()
	out := make(map[string]coderuntime.LSPClientInfo, len(states))
	for k, v := range states {
		out[k] = coderuntime.LSPClientInfo{
			Name: v.Name, State: v.State, Error: v.Error,
			DiagnosticCount: v.DiagnosticCount, ConnectedAt: v.ConnectedAt,
		}
	}
	return out
}

func (w *crushToolbox) LSPDiagnostics(name string) any { return w.tb.LSPDiagnostics(name) }

func (w *crushToolbox) StartLSP(ctx context.Context, path string) { w.tb.StartLSP(ctx, path) }

func (w *crushToolbox) StopAllLSPs(ctx context.Context) { w.tb.StopAllLSPs(ctx) }

func (w *crushToolbox) SetLSPEventListener(fn func(e coderuntime.LSPEvent)) {
	if fn == nil {
		w.tb.SetLSPEventListener(nil)
		return
	}
	w.tb.SetLSPEventListener(func(e ct.LSPEvent) {
		fn(coderuntime.LSPEvent{
			Type: e.Type, Name: e.Name, State: e.State, Error: e.Error,
			DiagnosticCount: e.DiagnosticCount,
		})
	})
}

func (w *crushToolbox) Close() { w.tb.Close() }
