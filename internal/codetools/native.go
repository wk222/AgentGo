package codetools

import (
	"context"
	"encoding/json"
	"errors"

	"agentgo/internal/codelsp"
	"agentgo/internal/coderuntime"
	"agentgo/internal/config"
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

// SymbolSearch adapts the native grep walker for codelsp: lsp_references,
// lsp_definition and lsp_call_hierarchy find a symbol's candidate positions
// with the same ripgrep-first search and ignore rules the grep tool uses.
//
// It applies the same gate as the grep tool: codelsp derives the root from the
// model's path argument, so without this a symbol query reads files outside the
// workspace (and returns their matching lines as candidates) with nobody asked.
func SymbolSearch(opts SearchOptions) codelsp.SymbolSearch {
	s := &searcher{normalizeSearch(opts)}
	return func(ctx context.Context, pattern, root string, _ int) ([]codelsp.SymbolHit, error) {
		root = s.resolveRoot(root)
		if msg, ok := s.allowRoot(ctx, "", "", "lsp_symbols", "search", root); !ok {
			return nil, errors.New(msg)
		}
		ctx, cancel := context.WithTimeout(ctx, s.GrepTimeout)
		defer cancel()
		matches, _, err := s.searchFiles(ctx, pattern, root, "")
		if err != nil {
			return nil, err
		}
		hits := make([]codelsp.SymbolHit, 0, len(matches))
		for _, m := range matches {
			hits = append(hits, codelsp.SymbolHit{Path: m.path, Line: m.lineNum, Char: m.charNum})
		}
		return hits, nil
	}
}

// NativeFS builds the native view/write/edit/multiedit tools for workDir.
// Language-server work is still done by base: view warms the server up, and a
// successful change appends base's lsp_diagnostics for the changed file, which
// is what the Crush tools did inline. approve may be nil (allow all).
func NativeFS(base coderuntime.Toolbox, workDir string, approve func(context.Context, coderuntime.FSRequest) bool) []coderuntime.Tool {
	var journal *coderuntime.Journal
	if config.OverwriteBackup.On() {
		journal = coderuntime.OpenJournal(workDir)
	}
	return coderuntime.NewFSTools(coderuntime.FSOptions{
		WorkDir: workDir,
		Approve: approve,
		// AGENTGO_OVERWRITE_BACKUP=0 turns the pre-overwrite backup, and with it the
		// change journal behind file_changes / revert_file_changes, off.
		DisableBackup: !config.OverwriteBackup.On(),
		Journal:       journal,
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
