package codelsp

// The lsp_* tools: what the model can ask a language server. This file holds the
// registry and the pieces shared by the tools; navigation lives in tools_nav.go
// and diagnostics in tools_diag.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"agentgo/internal/coderuntime"
)

// SymbolHit is one text match for a symbol name; Line and Char are 1-based.
type SymbolHit struct {
	Path       string
	Line, Char int
}

// SymbolSearch finds candidate positions for a symbol name under root. The code
// tools plug their grep in here, so symbol lookup and grep share one walker and
// one set of ignore rules.
type SymbolSearch func(ctx context.Context, pattern, root string, limit int) ([]SymbolHit, error)

// toolset carries what every tool needs.
type toolset struct {
	sv     *supervisor
	root   string
	search SymbolSearch
}

func newTools(sv *supervisor, root string, search SymbolSearch) []coderuntime.Tool {
	ts := &toolset{sv: sv, root: root, search: search}
	return []coderuntime.Tool{
		ts.diagnosticsTool(),
		ts.referencesTool(),
		ts.symbolsTool(),
		ts.definitionTool(),
		ts.hierarchyTool(),
	}
}

func reply(s string) coderuntime.ToolResult { return coderuntime.ToolResult{Content: s} }
func refuse(s string) coderuntime.ToolResult {
	return coderuntime.ToolResult{Content: s, IsError: true}
}
func textProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func schemaOf(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// bind decodes the model's JSON arguments. bad is true when they could not be
// decoded, in which case res is the message to return.
func bind(argsJSON string, into any) (res coderuntime.ToolResult, bad bool) {
	if strings.TrimSpace(argsJSON) == "" {
		argsJSON = "{}"
	}
	if err := json.Unmarshal([]byte(argsJSON), into); err != nil {
		return refuse("The arguments are not valid JSON for this tool: " + err.Error()), true
	}
	return coderuntime.ToolResult{}, false
}

// absPath anchors a model-supplied path at the workspace.
func (ts *toolset) absPath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(ts.root, p)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
