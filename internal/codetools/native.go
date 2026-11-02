package codetools

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"agentgo/internal/codelsp"
	"agentgo/internal/coderuntime"
	"agentgo/internal/config"
)

// Overlay returns a toolbox that serves native in place of the same-named
// tools of base (new names are appended). Everything else (LSP state, events,
// Close) still comes from base. It is how the file and search tools are layered
// on top of the language-server toolbox.
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

// clampInt keeps a setting inside the range the tools can honour.
func clampInt(v, lo, hi int) int { return min(max(v, lo), hi) }

// ConfiguredSearch fills in the search limits the user set with
// AGENTGO_GREP_TIMEOUT_SEC (1-300), AGENTGO_GLOB_TIMEOUT_SEC (1-300) and
// AGENTGO_SEARCH_LIMIT (20-500); a value outside its range is moved to the
// nearest end. Limits already set in opts are kept.
func ConfiguredSearch(opts SearchOptions) SearchOptions {
	if opts.GrepTimeout == 0 {
		opts.GrepTimeout = time.Duration(clampInt(config.GrepTimeoutSec.Get(), 1, 300)) * time.Second
	}
	if opts.GlobTimeout == 0 {
		opts.GlobTimeout = time.Duration(clampInt(config.GlobTimeoutSec.Get(), 1, 300)) * time.Second
	}
	if opts.Limit == 0 {
		opts.Limit = clampInt(config.SearchLimit.Get(), 20, 500)
	}
	return opts
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
		res, err := s.searchFiles(ctx, pattern, root, "", poolFor(s.Limit))
		if err != nil {
			return nil, err
		}
		matches := res.matches
		if len(matches) > s.Limit {
			matches = matches[:s.Limit]
		}
		hits := make([]codelsp.SymbolHit, 0, len(matches))
		for _, m := range matches {
			hits = append(hits, codelsp.SymbolHit{Path: m.path, Line: m.lineNum, Char: m.charNum})
		}
		return hits, nil
	}
}

// NativeFS builds the view/write/edit/multiedit tools for workDir. Language-
// server work is delegated to base: view warms the server up, and a successful
// change appends base's lsp_diagnostics for the changed file. approve may be nil
// (allow all).
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
		// AGENTGO_VIEW_LINES: how many lines view shows when the model gives no limit.
		ReadLimit: clampInt(config.ViewLines.Get(), 20, 2000),
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
