package codelsp

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"

	"agentgo/internal/coderuntime"
)

// SymbolHit is one grep match for a symbol name; Line and Char are 1-based.
type SymbolHit struct {
	Path       string
	Line, Char int
}

// SymbolSearch finds candidate positions for a symbol name under root. The
// code tools plug their grep in here, so symbol lookup and grep share one
// walker and one set of ignore rules.
type SymbolSearch func(ctx context.Context, pattern, root string, limit int) ([]SymbolHit, error)

type tools struct {
	m       *Manager
	workDir string
	search  SymbolSearch
}

func ok(s string) coderuntime.ToolResult  { return coderuntime.ToolResult{Content: s} }
func bad(s string) coderuntime.ToolResult { return coderuntime.ToolResult{Content: s, IsError: true} }

func prop(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

func object(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func decode(argsJSON string, into any) (coderuntime.ToolResult, bool) {
	if strings.TrimSpace(argsJSON) == "" {
		argsJSON = "{}"
	}
	if err := json.Unmarshal([]byte(argsJSON), into); err != nil {
		return bad("invalid arguments: " + err.Error()), true
	}
	return coderuntime.ToolResult{}, false
}

// newTools builds the five lsp_* tools.
func newTools(m *Manager, workDir string, search SymbolSearch) []coderuntime.Tool {
	t := &tools{m: m, workDir: workDir, search: search}
	symbolPath := prop("string", "The directory to search in. Defaults to the current working directory.")
	return []coderuntime.Tool{
		{
			Name:        "lsp_diagnostics",
			Description: "Get LSP errors, warnings, and hints for a file or the whole project.",
			Schema: object(map[string]any{
				"file_path": prop("string", "The path to the file to get diagnostics for (leave empty for project diagnostics)"),
			}),
			Run: func(ctx context.Context, _, _, args string) (coderuntime.ToolResult, error) {
				var a struct {
					FilePath string `json:"file_path"`
				}
				if r, b := decode(args, &a); b {
					return r, nil
				}
				t.notify(ctx, a.FilePath)
				return ok(t.diagnostics(a.FilePath)), nil
			},
		},
		{
			Name:        "lsp_references",
			Description: "Find all references to a symbol by name via LSP; more accurate than grep for code symbols.",
			Schema: object(map[string]any{
				"symbol": prop("string", "The symbol name to search for (e.g., function name, variable name, type name)"),
				"path":   symbolPath,
			}, "symbol"),
			Run: func(ctx context.Context, _, _, args string) (coderuntime.ToolResult, error) {
				var a symbolArgs
				if r, b := decode(args, &a); b {
					return r, nil
				}
				return t.references(ctx, a), nil
			},
		},
		{
			Name: "lsp_symbols",
			Description: "List document symbols (functions, types, methods, etc.) for a file via LSP. Returns a structured outline showing symbol names, kinds, and line numbers. " +
				"Use this before editing unfamiliar files to understand structure.",
			Schema: object(map[string]any{"file_path": prop("string", "The path to the file to get symbols for")}, "file_path"),
			Run: func(ctx context.Context, _, _, args string) (coderuntime.ToolResult, error) {
				var a struct {
					FilePath string `json:"file_path"`
				}
				if r, b := decode(args, &a); b {
					return r, nil
				}
				return t.symbols(ctx, a.FilePath), nil
			},
		},
		{
			Name: "lsp_definition",
			Description: "Find the definition of a symbol by name via LSP. Prefer this over grep for finding where something is defined: it is language-aware and skips matches in comments, strings, and partial identifiers. " +
				"Returns the file path, line number, and surrounding context. Complements lsp_references (which finds usages).",
			Schema: object(map[string]any{"symbol": prop("string", "The symbol name to find the definition of"), "path": symbolPath}, "symbol"),
			Run: func(ctx context.Context, _, _, args string) (coderuntime.ToolResult, error) {
				var a symbolArgs
				if r, b := decode(args, &a); b {
					return r, nil
				}
				return t.definition(ctx, a), nil
			},
		},
		{
			Name: "lsp_call_hierarchy",
			Description: "Show the call hierarchy for a symbol: either incoming calls (who calls this) or outgoing calls (what does this call). " +
				"Use before refactoring to understand blast radius, or when tracing execution flow. Returns the callers/callees with file paths and line numbers.",
			Schema: object(map[string]any{
				"symbol":    prop("string", "The symbol name to show call hierarchy for"),
				"direction": prop("string", "Either 'incoming' (who calls this) or 'outgoing' (what does this call)"),
				"path":      symbolPath,
			}, "symbol", "direction"),
			Run: func(ctx context.Context, _, _, args string) (coderuntime.ToolResult, error) {
				var a struct {
					symbolArgs
					Direction string `json:"direction"`
				}
				if r, b := decode(args, &a); b {
					return r, nil
				}
				return t.callHierarchy(ctx, a.symbolArgs, a.Direction), nil
			},
		},
	}
}

type symbolArgs struct {
	Symbol string `json:"symbol"`
	Path   string `json:"path"`
}

// --- symbol resolution -------------------------------------------------------

type resolved struct {
	client *Client
	path   string
	line   int
	char   int
}

func isNoIdentifier(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no identifier found")
}

// symbolOffset is where the bare name starts inside a qualified symbol
// ("Bar" in "foo.Bar", "method" in "Class::method").
func symbolOffset(sym string) int {
	if i := strings.LastIndex(sym, "::"); i != -1 {
		return i + 2
	}
	for _, sep := range []string{".", `\`} {
		if i := strings.LastIndex(sym, sep); i != -1 {
			return i + 1
		}
	}
	return 0
}

func (t *tools) searchRoot(p string) string {
	if p == "" {
		return t.workDir
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(t.workDir, p)
}

// candidates greps for the symbol, warms the servers up and returns every
// position some running server handles.
func (t *tools) candidates(ctx context.Context, symbol, root string) ([]*resolved, error) {
	t.m.Start(ctx, root)
	if t.search == nil {
		return nil, errors.New("symbol search is not available")
	}
	// Word boundaries keep "Bar" from matching inside "myBar".
	hits, err := t.search(ctx, `\b`+regexp.QuoteMeta(symbol)+`\b`, root, 100)
	if err != nil {
		return nil, fmt.Errorf("failed to search for symbol: %w", err)
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("symbol '%s' not found in grep results", symbol)
	}
	var out []*resolved
	for _, h := range hits {
		abs, err := filepath.Abs(h.Path)
		if err != nil {
			continue
		}
		if c := t.m.FindClient(abs); c != nil {
			out = append(out, &resolved{client: c, path: abs, line: h.Line, char: h.Char + symbolOffset(symbol)})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no LSP client handles any file matching '%s'", symbol)
	}
	return out, nil
}

// resolve picks the first candidate the server can resolve to a definition.
// Matches in comments, strings and partial identifiers are rejected by the
// server, either with a "no identifier" error or (gopls in a comment) with an
// empty answer; both mean "not a real use of the symbol". Other errors are
// let through so the caller reports them.
func (t *tools) resolve(ctx context.Context, symbol, root string) (*resolved, error) {
	all, err := t.candidates(ctx, symbol, root)
	if err != nil {
		return nil, err
	}
	for _, r := range all {
		locs, err := r.client.Definition(ctx, r.path, r.line, r.char)
		if err == nil && len(locs) > 0 || err != nil && !isNoIdentifier(err) {
			return r, nil
		}
	}
	return all[0], nil
}

// --- tools ------------------------------------------------------------------

func (t *tools) references(ctx context.Context, a symbolArgs) coderuntime.ToolResult {
	if a.Symbol == "" {
		return bad("symbol is required")
	}
	all, err := t.candidates(ctx, a.Symbol, t.searchRoot(a.Path))
	if err != nil {
		return ok(fmt.Sprintf("Symbol '%s' not found", a.Symbol))
	}
	var locs []protocol.Location
	var errs error
	for _, r := range all {
		got, err := r.client.FindReferences(ctx, r.path, r.line, r.char, true)
		if err != nil {
			if isNoIdentifier(err) {
				continue
			}
			slog.Error("lsp: references", "error", err, "symbol", a.Symbol, "path", r.path)
			errs = errors.Join(errs, err)
			continue
		}
		locs = append(locs, got...)
		if len(got) > 0 { // a server returns every reference, not just this file's
			break
		}
	}
	if len(locs) > 0 {
		return ok(formatReferences(cleanupLocations(locs)))
	}
	if errs != nil {
		return bad(errs.Error())
	}
	return ok(fmt.Sprintf("No references found for symbol '%s'", a.Symbol))
}

func (t *tools) definition(ctx context.Context, a symbolArgs) coderuntime.ToolResult {
	if a.Symbol == "" {
		return bad("symbol is required")
	}
	r, err := t.resolve(ctx, a.Symbol, t.searchRoot(a.Path))
	if err != nil {
		return bad(fmt.Sprintf("Symbol '%s' not found", a.Symbol))
	}
	locs, err := r.client.Definition(ctx, r.path, r.line, r.char)
	if err != nil {
		if isNoIdentifier(err) {
			return ok(fmt.Sprintf("No definition found for symbol '%s'", a.Symbol))
		}
		slog.Error("lsp: definition", "error", err, "symbol", a.Symbol)
		return bad(fmt.Sprintf("definition lookup failed: %s", err))
	}
	if len(locs) == 0 {
		return bad(fmt.Sprintf("No definition found for symbol '%s'", a.Symbol))
	}
	return ok(formatDefinitions(locs))
}

func (t *tools) symbols(ctx context.Context, file string) coderuntime.ToolResult {
	if file == "" {
		return bad("file_path is required")
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(t.workDir, file)
	}
	t.m.Start(ctx, file)
	c := t.m.FindClient(file)
	if c == nil {
		return bad(fmt.Sprintf("no LSP client handles file: %s", file))
	}
	syms, err := c.DocumentSymbols(ctx, file)
	if err != nil {
		return bad(fmt.Sprintf("failed to get document symbols: %s", err))
	}
	if len(syms) == 0 {
		return ok(fmt.Sprintf("No symbols found in %s", file))
	}
	return ok(formatSymbols(syms, 0))
}

func (t *tools) callHierarchy(ctx context.Context, a symbolArgs, direction string) coderuntime.ToolResult {
	if a.Symbol == "" {
		return bad("symbol is required")
	}
	if direction != "incoming" && direction != "outgoing" {
		return bad("direction must be 'incoming' or 'outgoing'")
	}
	r, err := t.resolve(ctx, a.Symbol, t.searchRoot(a.Path))
	if err != nil {
		return bad(fmt.Sprintf("Symbol '%s' not found", a.Symbol))
	}
	items, err := r.client.PrepareCallHierarchy(ctx, r.path, r.line, r.char)
	if err != nil {
		return bad(fmt.Sprintf("failed to prepare call hierarchy: %s", err))
	}
	if len(items) == 0 {
		return bad(fmt.Sprintf("No call hierarchy information for '%s'", a.Symbol))
	}
	item := items[0]

	var b strings.Builder
	fmt.Fprintf(&b, "Call hierarchy for '%s':\n\n", item.Name)
	line := func(u protocol.DocumentURI, rng protocol.Range, name string) {
		p, _ := u.Path()
		fmt.Fprintf(&b, "  %s:%d — %s\n", p, rng.Start.Line+1, name)
	}
	if direction == "incoming" {
		calls, err := r.client.IncomingCalls(ctx, item)
		if err != nil {
			return bad(fmt.Sprintf("failed to get incoming calls: %s", err))
		}
		if len(calls) == 0 {
			b.WriteString("No incoming calls found.\n")
		} else {
			fmt.Fprintf(&b, "%d caller(s):\n\n", len(calls))
			for _, c := range calls {
				line(c.From.URI, c.From.Range, c.From.Name)
			}
		}
		return ok(b.String())
	}
	calls, err := r.client.OutgoingCalls(ctx, item)
	if err != nil {
		return bad(fmt.Sprintf("failed to get outgoing calls: %s", err))
	}
	if len(calls) == 0 {
		b.WriteString("No outgoing calls found.\n")
	} else {
		fmt.Fprintf(&b, "%d callee(s):\n\n", len(calls))
		for _, c := range calls {
			line(c.To.URI, c.To.Range, c.To.Name)
		}
	}
	return ok(b.String())
}

// --- diagnostics -----------------------------------------------------------------

// notify tells the servers a file changed and waits for fresh diagnostics. An
// empty path refreshes every open file of every server.
func (t *tools) notify(ctx context.Context, path string) {
	if path == "" {
		var wg sync.WaitGroup
		for _, c := range t.m.Clients() {
			wg.Go(func() {
				c.RefreshOpenFiles(ctx)
				if err := c.NotifyWorkspaceChange(ctx); err != nil {
					slog.Warn("lsp: workspace change", "error", err)
				}
				c.WaitForDiagnostics(ctx, 5*time.Second)
			})
		}
		wg.Wait()
		return
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(t.workDir, path)
	}
	t.m.Start(ctx, path)
	var wg sync.WaitGroup
	for _, c := range t.m.Clients() {
		if !c.HandlesFile(path) {
			continue
		}
		_ = c.OpenOnDemand(ctx, path)
		_ = c.NotifyChange(ctx, path)
		wg.Go(func() { c.WaitForDiagnostics(ctx, 5*time.Second) })
	}
	wg.Wait()
}

func (t *tools) diagnostics(filePath string) string {
	if filePath != "" && !filepath.IsAbs(filePath) {
		filePath = filepath.Join(t.workDir, filePath)
	}
	var fileDiags, projectDiags []string
	for _, c := range t.m.Clients() {
		for uri, ds := range c.Diagnostics() {
			p, err := uri.Path()
			if err != nil {
				continue
			}
			for _, d := range ds {
				f := formatDiagnostic(p, d, c.name)
				if p == filePath {
					fileDiags = append(fileDiags, f)
				} else {
					projectDiags = append(projectDiags, f)
				}
			}
		}
	}
	sortDiagnostics(fileDiags)
	sortDiagnostics(projectDiags)

	var out strings.Builder
	writeDiagnostics(&out, "file_diagnostics", fileDiags)
	writeDiagnostics(&out, "project_diagnostics", projectDiags)
	if len(fileDiags) > 0 || len(projectDiags) > 0 {
		out.WriteString("\n<diagnostic_summary>\n")
		fmt.Fprintf(&out, "Current file: %d errors, %d warnings\n", countSeverity(fileDiags, "Error"), countSeverity(fileDiags, "Warn"))
		fmt.Fprintf(&out, "Project: %d errors, %d warnings\n", countSeverity(projectDiags, "Error"), countSeverity(projectDiags, "Warn"))
		out.WriteString("</diagnostic_summary>\n")
	}
	return out.String()
}

func writeDiagnostics(out *strings.Builder, tag string, in []string) {
	if len(in) == 0 {
		return
	}
	out.WriteString("\n<" + tag + ">\n")
	if len(in) > 10 {
		out.WriteString(strings.Join(in[:10], "\n"))
		fmt.Fprintf(out, "\n... and %d more diagnostics", len(in)-10)
	} else {
		out.WriteString(strings.Join(in, "\n"))
	}
	out.WriteString("\n</" + tag + ">\n")
}

func sortDiagnostics(in []string) {
	sort.Slice(in, func(i, j int) bool {
		ie, je := strings.HasPrefix(in[i], "Error"), strings.HasPrefix(in[j], "Error")
		if ie != je {
			return ie
		}
		return in[i] < in[j]
	})
}

func formatDiagnostic(path string, d protocol.Diagnostic, source string) string {
	severity := "Info"
	switch d.Severity {
	case protocol.SeverityError:
		severity = "Error"
	case protocol.SeverityWarning:
		severity = "Warn"
	case protocol.SeverityHint:
		severity = "Hint"
	}
	src := source
	if d.Source != "" {
		src += " " + d.Source
	}
	code := ""
	if d.Code != nil {
		code = fmt.Sprintf("[%v]", d.Code)
	}
	var tags []string
	for _, tag := range d.Tags {
		switch tag {
		case protocol.Unnecessary:
			tags = append(tags, "unnecessary")
		case protocol.Deprecated:
			tags = append(tags, "deprecated")
		}
	}
	tagInfo := ""
	if len(tags) > 0 {
		tagInfo = fmt.Sprintf(" (%s)", strings.Join(tags, ", "))
	}
	return fmt.Sprintf("%s: %s:%d:%d [%s]%s%s %s", severity, path,
		d.Range.Start.Line+1, d.Range.Start.Character+1, src, code, tagInfo, d.Message)
}

func countSeverity(in []string, severity string) int {
	n := 0
	for _, s := range in {
		if strings.HasPrefix(s, severity) {
			n++
		}
	}
	return n
}

// --- formatting ----------------------------------------------------------------------

func cleanupLocations(locs []protocol.Location) []protocol.Location {
	slices.SortFunc(locs, func(a, b protocol.Location) int {
		if a.URI != b.URI {
			return strings.Compare(string(a.URI), string(b.URI))
		}
		if a.Range.Start.Line != b.Range.Start.Line {
			return cmp.Compare(a.Range.Start.Line, b.Range.Start.Line)
		}
		return cmp.Compare(a.Range.Start.Character, b.Range.Start.Character)
	})
	return slices.CompactFunc(locs, func(a, b protocol.Location) bool {
		return a.URI == b.URI && a.Range.Start.Line == b.Range.Start.Line && a.Range.Start.Character == b.Range.Start.Character
	})
}

func formatReferences(locs []protocol.Location) string {
	byFile := map[string][]protocol.Location{}
	for _, l := range locs {
		p, err := l.URI.Path()
		if err != nil {
			continue
		}
		byFile[p] = append(byFile[p], l)
	}
	files := slices.Sorted(maps.Keys(byFile))
	var out strings.Builder
	fmt.Fprintf(&out, "Found %d reference(s) in %d file(s):\n\n", len(locs), len(files))
	for _, f := range files {
		refs := byFile[f]
		fmt.Fprintf(&out, "%s (%d reference(s)):\n", f, len(refs))
		for _, r := range refs {
			fmt.Fprintf(&out, "  Line %d, Column %d\n", r.Range.Start.Line+1, r.Range.Start.Character+1)
		}
		out.WriteString("\n")
	}
	return out.String()
}

func formatDefinitions(locs []protocol.Location) string {
	locs = cleanupLocations(locs)
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d definition(s):\n\n", len(locs))
	for _, l := range locs {
		p, err := l.URI.Path()
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "%s:%d\n", p, l.Range.Start.Line+1)
		if snip := sourceContext(p, int(l.Range.Start.Line), 3); snip != "" {
			b.WriteString(snip)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// sourceContext renders the lines around target with a ">" marker on it.
func sourceContext(path string, target, around int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	var b strings.Builder
	for i := max(0, target-around); i < min(len(lines), target+around+1); i++ {
		marker := "  "
		if i == target {
			marker = "> "
		}
		fmt.Fprintf(&b, "%s%4d | %s\n", marker, i+1, lines[i])
	}
	return b.String()
}

func formatSymbols(symbols []protocol.DocumentSymbolResult, indent int) string {
	var b strings.Builder
	prefix := strings.Repeat("  ", indent)
	for _, s := range symbols {
		fmt.Fprintf(&b, "%s%s %s (line %d)\n", prefix, symbolKind(s), s.GetName(), s.GetRange().Start.Line+1)
		if ds, ok := s.(*protocol.DocumentSymbol); ok && len(ds.Children) > 0 {
			b.WriteString(formatChildren(ds.Children, indent+1))
		}
	}
	return b.String()
}

func formatChildren(children []protocol.DocumentSymbol, indent int) string {
	var b strings.Builder
	prefix := strings.Repeat("  ", indent)
	for i := range children {
		c := &children[i]
		fmt.Fprintf(&b, "%s%s %s (line %d)\n", prefix, symbolKind(c), c.Name, c.Range.Start.Line+1)
		if len(c.Children) > 0 {
			b.WriteString(formatChildren(c.Children, indent+1))
		}
	}
	return b.String()
}

func symbolKind(sym protocol.DocumentSymbolResult) string {
	var k protocol.SymbolKind
	switch s := sym.(type) {
	case *protocol.DocumentSymbol:
		k = s.Kind
	case *protocol.SymbolInformation:
		k = s.Kind
	default:
		return "Symbol"
	}
	if n, ok := symbolKindNames[k]; ok {
		return n
	}
	return "Symbol"
}

var symbolKindNames = map[protocol.SymbolKind]string{
	protocol.File: "File", protocol.Module: "Module", protocol.Namespace: "Namespace", protocol.Package: "Package",
	protocol.Class: "Class", protocol.Method: "Method", protocol.Property: "Property", protocol.Field: "Field",
	protocol.Constructor: "Constructor", protocol.Enum: "Enum", protocol.Interface: "Interface",
	protocol.Function: "Function", protocol.Variable: "Variable", protocol.Constant: "Constant",
	protocol.String: "String", protocol.Number: "Number", protocol.Boolean: "Boolean", protocol.Array: "Array",
	protocol.Object: "Object", protocol.Key: "Key", protocol.Null: "Null", protocol.EnumMember: "EnumMember",
	protocol.Struct: "Struct", protocol.Event: "Event", protocol.Operator: "Operator", protocol.TypeParameter: "TypeParameter",
}
