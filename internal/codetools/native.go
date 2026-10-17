package codetools

import (
	"context"
	"encoding/json"

	"agentgo/internal/coderuntime"
)

// Overlay returns a toolbox that serves native in place of the same-named
// tools of base (new names are appended). Everything else (LSP state, events,
// Close) still comes from base. It is how the native file tools replace the
// Crush ones one at a time without touching the rest of the toolbox.
func Overlay(base coderuntime.Toolbox, native []coderuntime.Tool) coderuntime.Toolbox {
	return &overlay{Toolbox: base, native: native}
}

type overlay struct {
	coderuntime.Toolbox
	native []coderuntime.Tool
}

func (o *overlay) Tools() []coderuntime.Tool {
	byName := make(map[string]coderuntime.Tool, len(o.native))
	for _, t := range o.native {
		byName[t.Name] = t
	}
	var out []coderuntime.Tool
	used := map[string]bool{}
	for _, t := range o.Toolbox.Tools() {
		if n, ok := byName[t.Name]; ok {
			out = append(out, n)
			used[t.Name] = true
			continue
		}
		out = append(out, t)
	}
	for _, t := range o.native {
		if !used[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

// NativeFS builds the native view/write/edit/multiedit tools for workDir.
// Language-server work is still done by base: view warms the server up, and a
// successful change appends base's lsp_diagnostics for the changed file, which
// is what the Crush tools did inline. approve may be nil (allow all).
func NativeFS(base coderuntime.Toolbox, workDir string, approve func(context.Context, coderuntime.FSRequest) bool) []coderuntime.Tool {
	return coderuntime.NewFSTools(coderuntime.FSOptions{
		WorkDir: workDir,
		Approve: approve,
		OnRead: func(ctx context.Context, path string) {
			base.StartLSP(ctx, path)
		},
		AfterChange: func(ctx context.Context, sessionID, callID, path string) string {
			for _, t := range base.Tools() {
				if t.Name != "lsp_diagnostics" {
					continue
				}
				args, _ := json.Marshal(map[string]string{"file_path": path})
				r, err := t.Run(ctx, sessionID, callID, string(args))
				if err != nil {
					return ""
				}
				return r.Content
			}
			return ""
		},
	})
}
