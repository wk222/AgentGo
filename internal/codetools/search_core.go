package codetools

// Native grep, glob and ls.
//
// Search strategy: ripgrep when it is on PATH, otherwise a pure-Go walk that
// applies the same ignore rules (search_walk.go, search_ignore.go). Both
// backends feed the same ordering and the same result layout, so the model sees
// one behavior whichever engine ran.

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"agentgo/internal/coderuntime"
)

const (
	grepLimit      = 100  // matches one grep call returns
	globLimit      = 100  // paths one glob call returns
	lsLimit        = 1000 // entries one ls call returns
	maxMatchWidth  = 500  // characters of a matching line that are shown
	poolPerResult  = 50   // candidates gathered per returned result before cutting
	declinedByUser = "The user declined this request; nothing was done."
)

// SearchOptions configures NativeSearch.
type SearchOptions struct {
	WorkDir string
	// Approve gates searching or listing outside WorkDir. nil allows all.
	Approve func(ctx context.Context, req coderuntime.FSRequest) bool
	// Rg returns the ripgrep binary, or "" to use the pure-Go walker. nil means
	// look "rg" up on PATH.
	Rg          func() string
	GrepTimeout time.Duration // default 5s
	GlobTimeout time.Duration // default 30s
}

type searcher struct{ SearchOptions }

// normalizeSearch fills in the defaults: rg from PATH, 5s grep and 30s glob.
func normalizeSearch(opts SearchOptions) SearchOptions {
	if opts.Rg == nil {
		opts.Rg = func() string { p, _ := exec.LookPath("rg"); return p }
	}
	opts.GrepTimeout = cmp.Or(opts.GrepTimeout, 5*time.Second)
	opts.GlobTimeout = cmp.Or(opts.GlobTimeout, 30*time.Second)
	return opts
}

// NativeSearch returns the grep, glob and ls tools.
func NativeSearch(opts SearchOptions) []coderuntime.Tool {
	s := &searcher{normalizeSearch(opts)}
	return []coderuntime.Tool{s.grepTool(), s.globTool(), s.lsTool()}
}

func toolErr(s string) coderuntime.ToolResult {
	return coderuntime.ToolResult{Content: s, IsError: true}
}

func toolOK(s string) coderuntime.ToolResult { return coderuntime.ToolResult{Content: s} }

func objSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func prop(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

func decode(argsJSON string, into any) (coderuntime.ToolResult, bool) {
	if strings.TrimSpace(argsJSON) == "" {
		argsJSON = "{}"
	}
	if err := json.Unmarshal([]byte(argsJSON), into); err != nil {
		return toolErr("The arguments are not valid JSON for this tool: " + err.Error()), true
	}
	return coderuntime.ToolResult{}, false
}

// resolveRoot turns the model's "path" argument into the tree to search. No
// path means the workspace and a relative one hangs off it: anything else would
// search the process working directory, which has nothing to do with the folder
// the user opened. The three search tools all resolve this way.
func (s *searcher) resolveRoot(p string) string {
	p = strings.TrimSpace(expandPath(p))
	switch {
	case p == "":
		return s.WorkDir
	case isRooted(p), s.WorkDir == "":
		return p
	}
	return filepath.Join(s.WorkDir, p)
}

// isRooted reports whether p is absolute, counting a leading slash as absolute
// on Windows too (models write POSIX-style paths there).
func isRooted(p string) bool {
	if filepath.IsAbs(p) {
		return true
	}
	return runtime.GOOS == "windows" && strings.HasPrefix(filepath.ToSlash(p), "/")
}

// outsideWorkDir reports whether abs lies outside the workspace; known is false
// when no workspace is set or the paths cannot be compared.
func (s *searcher) outsideWorkDir(abs string) (outside, known bool) {
	if strings.TrimSpace(s.WorkDir) == "" {
		return false, false
	}
	root, err := filepath.Abs(s.WorkDir)
	if err != nil {
		return false, false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return true, true
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)), true
}

// allowRoot is the gate in front of every search and listing: looking outside
// the workspace needs approval, otherwise these tools would read any file on the
// machine even when code-tool actions are switched off. On refusal it returns
// the text to show the model.
func (s *searcher) allowRoot(ctx context.Context, sessionID, callID, tool, action, root string) (string, bool) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Sprintf("Cannot resolve the search path %q: %v", root, err), false
	}
	if outside, known := s.outsideWorkDir(abs); !known || !outside {
		return "", true
	}
	if s.Approve == nil {
		return "", true
	}
	req := coderuntime.FSRequest{SessionID: sessionID, CallID: callID, Tool: tool, Action: action, Path: abs,
		Description: "Look outside the working directory: " + abs}
	if !s.Approve(ctx, req) {
		return declinedByUser, false
	}
	return "", true
}
