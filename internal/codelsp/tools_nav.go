package codelsp

// Navigation tools: references, definition, outline and call hierarchy. The
// model names a symbol; a text search finds where the name appears and a
// language server is asked about those positions.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"

	"agentgo/internal/coderuntime"
)

type symbolArgs struct {
	Symbol string `json:"symbol"`
	Path   string `json:"path"`
}

var (
	errNoSearch   = errors.New("symbol lookup is not wired to a text search in this build")
	errNameAbsent = errors.New("the name does not appear in any file")
	errNoServer   = errors.New("no running language server handles the files that mention it")
)

const maxCandidates = 100

func pathProp() map[string]any {
	return textProp("Folder to look in; defaults to the working directory")
}

// explainLookup turns a lookup failure into the text the model sees.
func explainLookup(err error, symbol string) string {
	switch {
	case errors.Is(err, errNameAbsent):
		return fmt.Sprintf("Could not find %q: it does not appear in any searched file. Check the spelling or widen `path`.", symbol)
	case errors.Is(err, errNoServer):
		return fmt.Sprintf("%q appears in the code, but no language server is running for those files, so it cannot be analysed. Use grep instead.", symbol)
	}
	return fmt.Sprintf("Could not look up %q: %v", symbol, err)
}

func (ts *toolset) searchFrom(p string) string {
	if p == "" {
		return ts.root
	}
	return ts.absPath(p)
}

// nameOffset is where the bare name starts inside a qualified one: "Bar" in
// "pkg.Bar", "method" in "Type::method".
func nameOffset(symbol string) int {
	if i := strings.LastIndex(symbol, "::"); i >= 0 {
		return i + 2
	}
	best := -1
	for _, sep := range []string{".", `\`} {
		best = max(best, strings.LastIndex(symbol, sep))
	}
	return best + 1
}

// isNoIdentifier recognises the server's "nothing here to analyse" answer, which
// is how a match inside a comment, string or longer word is rejected.
func isNoIdentifier(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no identifier found")
}

// site is a position a running server can be asked about.
type site struct {
	sess      *session
	path      string
	line, col int
}

// sites finds every textual occurrence of name under root that lies in a file
// some running server handles. Servers for the languages involved are started
// first, one launch per file extension.
func (ts *toolset) sites(ctx context.Context, name, root string) ([]site, error) {
	if ts.search == nil {
		return nil, errNoSearch
	}
	// Word boundaries keep "Bar" from matching inside "myBar".
	hits, err := ts.search(ctx, `\b`+regexp.QuoteMeta(name)+`\b`, root, maxCandidates)
	if err != nil {
		return nil, fmt.Errorf("text search failed: %w", err)
	}
	if len(hits) == 0 {
		return nil, errNameAbsent
	}

	launched := map[string]bool{}
	for _, h := range hits {
		ext := strings.ToLower(filepath.Ext(h.Path))
		if launched[ext] {
			continue
		}
		launched[ext] = true
		if abs, err := filepath.Abs(h.Path); err == nil {
			ts.sv.ensure(ctx, abs)
		}
	}

	var out []site
	for _, h := range hits {
		abs, err := filepath.Abs(h.Path)
		if err != nil {
			continue
		}
		if s := ts.sv.serving(abs); s != nil {
			out = append(out, site{sess: s, path: abs, line: h.Line, col: h.Char + nameOffset(name)})
		}
	}
	if len(out) == 0 {
		return nil, errNoServer
	}
	return out, nil
}

// bestSite picks the first occurrence the server can resolve to a definition. A
// hit inside a comment or string is rejected by the server, either with a "no
// identifier" error or (gopls, in a comment) with an empty answer; neither is a
// real use of the symbol. Any other error is let through so the caller reports it.
func (ts *toolset) bestSite(ctx context.Context, name, root string) (site, error) {
	all, err := ts.sites(ctx, name, root)
	if err != nil {
		return site{}, err
	}
	for _, st := range all {
		locs, err := st.sess.definition(ctx, st.path, st.line, st.col)
		if (err == nil && len(locs) > 0) || (err != nil && !isNoIdentifier(err)) {
			return st, nil
		}
	}
	return all[0], nil
}

// --- references ----------------------------------------------------------------

func (ts *toolset) referencesTool() coderuntime.Tool {
	return coderuntime.Tool{
		Name: "lsp_references",
		Description: "List every place a symbol is used, found through the language server rather than by text search, " +
			"so comments, strings and look-alike names are excluded. Give the symbol's name; `path` limits where its first occurrence is looked for.",
		Schema: schemaOf(map[string]any{
			"symbol": textProp("Name of the function, type, variable or other symbol"),
			"path":   pathProp(),
		}, "symbol"),
		Run: func(ctx context.Context, _, _, args string) (coderuntime.ToolResult, error) {
			var a symbolArgs
			if r, bad := bind(args, &a); bad {
				return r, nil
			}
			return ts.references(ctx, a), nil
		},
	}
}

func (ts *toolset) references(ctx context.Context, a symbolArgs) coderuntime.ToolResult {
	if a.Symbol == "" {
		return refuse("symbol is required.")
	}
	all, err := ts.sites(ctx, a.Symbol, ts.searchFrom(a.Path))
	if err != nil {
		return refuse(explainLookup(err, a.Symbol))
	}
	var locs []protocol.Location
	var failures error
	for _, st := range all {
		got, err := st.sess.references(ctx, st.path, st.line, st.col)
		switch {
		case isNoIdentifier(err):
			continue
		case err != nil:
			slog.Error("lsp: references failed", "symbol", a.Symbol, "path", st.path, "error", err)
			failures = errors.Join(failures, err)
			continue
		}
		if len(got) > 0 {
			locs = got // a server answers for the whole project, so one non-empty answer is the answer
			break
		}
	}
	switch {
	case len(locs) > 0:
		return reply(renderReferences(a.Symbol, locs))
	case failures != nil:
		return refuse(failures.Error())
	}
	return reply(fmt.Sprintf("No references to %q were found.", a.Symbol))
}

// sortedUnique orders locations by file, line and column and drops duplicates.
func sortedUnique(locs []protocol.Location) []protocol.Location {
	locs = slices.Clone(locs)
	slices.SortFunc(locs, func(a, b protocol.Location) int {
		return cmp.Or(
			cmp.Compare(a.URI, b.URI),
			cmp.Compare(a.Range.Start.Line, b.Range.Start.Line),
			cmp.Compare(a.Range.Start.Character, b.Range.Start.Character),
		)
	})
	return slices.CompactFunc(locs, func(a, b protocol.Location) bool {
		return a.URI == b.URI && a.Range.Start == b.Range.Start
	})
}

func renderReferences(symbol string, locs []protocol.Location) string {
	locs = sortedUnique(locs)
	byFile := map[string][]protocol.Location{}
	for _, l := range locs {
		if p, err := l.URI.Path(); err == nil {
			byFile[p] = append(byFile[p], l)
		}
	}
	files := slices.Sorted(maps.Keys(byFile))
	var b strings.Builder
	fmt.Fprintf(&b, "%q: %s in %s\n", symbol, plural(len(locs), "reference", "references"), plural(len(files), "file", "files"))
	for _, f := range files {
		lines := newLineSource(f)
		fmt.Fprintf(&b, "\n%s\n", f)
		for _, l := range byFile[f] {
			row := int(l.Range.Start.Line)
			fmt.Fprintf(&b, "  %d:%d  %s\n", row+1, l.Range.Start.Character+1, strings.TrimSpace(lines.at(row)))
		}
	}
	return b.String()
}

// lineSource reads a file once and serves its lines by 0-based number.
type lineSource struct {
	lines []string
}

func newLineSource(path string) *lineSource {
	data, err := os.ReadFile(path)
	if err != nil {
		return &lineSource{}
	}
	return &lineSource{lines: strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")}
}

func (l *lineSource) at(row int) string {
	if row < 0 || row >= len(l.lines) {
		return ""
	}
	return l.lines[row]
}

// --- definition ----------------------------------------------------------------

func (ts *toolset) definitionTool() coderuntime.Tool {
	return coderuntime.Tool{
		Name: "lsp_definition",
		Description: "Find where a symbol is defined, by name. Language-aware, so it passes over matches in comments, strings and parts of longer identifiers, " +
			"and it shows a few lines around each definition. Complements lsp_references, which finds the uses.",
		Schema: schemaOf(map[string]any{
			"symbol": textProp("Name of the symbol to locate"),
			"path":   pathProp(),
		}, "symbol"),
		Run: func(ctx context.Context, _, _, args string) (coderuntime.ToolResult, error) {
			var a symbolArgs
			if r, bad := bind(args, &a); bad {
				return r, nil
			}
			return ts.definition(ctx, a), nil
		},
	}
}

func (ts *toolset) definition(ctx context.Context, a symbolArgs) coderuntime.ToolResult {
	if a.Symbol == "" {
		return refuse("symbol is required.")
	}
	st, err := ts.bestSite(ctx, a.Symbol, ts.searchFrom(a.Path))
	if err != nil {
		return refuse(explainLookup(err, a.Symbol))
	}
	locs, err := st.sess.definition(ctx, st.path, st.line, st.col)
	switch {
	case isNoIdentifier(err):
		return refuse(fmt.Sprintf("No definition found for %q.", a.Symbol))
	case err != nil:
		slog.Error("lsp: definition failed", "symbol", a.Symbol, "error", err)
		return refuse(fmt.Sprintf("The definition lookup failed: %v", err))
	case len(locs) == 0:
		return refuse(fmt.Sprintf("No definition found for %q.", a.Symbol))
	}
	return reply(renderDefinitions(a.Symbol, sortedUnique(locs)))
}

func renderDefinitions(symbol string, locs []protocol.Location) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q is defined in %s:\n", symbol, plural(len(locs), "place", "places"))
	for _, l := range locs {
		p, err := l.URI.Path()
		if err != nil {
			continue
		}
		row := int(l.Range.Start.Line)
		fmt.Fprintf(&b, "\n%s:%d\n", p, row+1)
		src := newLineSource(p)
		for r := max(0, row-2); r <= row+2 && r < len(src.lines); r++ {
			mark := "   "
			if r == row {
				mark = "-> "
			}
			fmt.Fprintf(&b, "%s%4d | %s\n", mark, r+1, src.at(r))
		}
	}
	return b.String()
}

// --- outline ---------------------------------------------------------------------

func (ts *toolset) symbolsTool() coderuntime.Tool {
	return coderuntime.Tool{
		Name: "lsp_symbols",
		Description: "Outline a source file: its functions, types, methods and other symbols, nested, each with its line number. " +
			"Read the outline before editing a file you do not know.",
		Schema: schemaOf(map[string]any{"file_path": textProp("File to outline")}, "file_path"),
		Run: func(ctx context.Context, _, _, args string) (coderuntime.ToolResult, error) {
			var a struct {
				FilePath string `json:"file_path"`
			}
			if r, bad := bind(args, &a); bad {
				return r, nil
			}
			return ts.outline(ctx, a.FilePath), nil
		},
	}
}

func (ts *toolset) outline(ctx context.Context, file string) coderuntime.ToolResult {
	if file == "" {
		return refuse("file_path is required.")
	}
	file = ts.absPath(file)
	ts.sv.ensure(ctx, file)
	s := ts.sv.serving(file)
	if s == nil {
		return refuse("No running language server handles " + file + ".")
	}
	syms, err := s.outline(ctx, file)
	switch {
	case err != nil:
		return refuse(fmt.Sprintf("The outline request failed: %v", err))
	case len(syms) == 0:
		return reply("No symbols found in " + file + ".")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Outline of %s:\n", file)
	for _, sym := range syms {
		writeOutline(&b, sym, 1)
	}
	return reply(b.String())
}

func writeOutline(b *strings.Builder, sym protocol.DocumentSymbolResult, depth int) {
	fmt.Fprintf(b, "%s%s %s  (line %d)\n", strings.Repeat("  ", depth), kindWord(sym), sym.GetName(), sym.GetRange().Start.Line+1)
	if ds, ok := sym.(*protocol.DocumentSymbol); ok {
		for i := range ds.Children {
			writeOutline(b, &ds.Children[i], depth+1)
		}
	}
}

func kindWord(sym protocol.DocumentSymbolResult) string {
	var k protocol.SymbolKind
	switch s := sym.(type) {
	case *protocol.DocumentSymbol:
		k = s.Kind
	case *protocol.SymbolInformation:
		k = s.Kind
	default:
		return "symbol"
	}
	if w, ok := symbolKinds[k]; ok {
		return w
	}
	return "symbol"
}

var symbolKinds = map[protocol.SymbolKind]string{
	protocol.File: "file", protocol.Module: "module", protocol.Namespace: "namespace", protocol.Package: "package",
	protocol.Class: "class", protocol.Method: "method", protocol.Property: "property", protocol.Field: "field",
	protocol.Constructor: "constructor", protocol.Enum: "enum", protocol.Interface: "interface",
	protocol.Function: "function", protocol.Variable: "variable", protocol.Constant: "constant",
	protocol.String: "string", protocol.Number: "number", protocol.Boolean: "boolean", protocol.Array: "array",
	protocol.Object: "object", protocol.Key: "key", protocol.Null: "null", protocol.EnumMember: "enum member",
	protocol.Struct: "struct", protocol.Event: "event", protocol.Operator: "operator", protocol.TypeParameter: "type parameter",
}

// --- call hierarchy --------------------------------------------------------------

func (ts *toolset) hierarchyTool() coderuntime.Tool {
	return coderuntime.Tool{
		Name: "lsp_call_hierarchy",
		Description: "Show who calls a function (direction \"incoming\") or what it calls (direction \"outgoing\"), each with file and line. " +
			"Use it before a refactor to see how far a change reaches, or to follow execution.",
		Schema: schemaOf(map[string]any{
			"symbol":    textProp("Name of the function or method"),
			"direction": textProp(`"incoming" for its callers, "outgoing" for what it calls`),
			"path":      pathProp(),
		}, "symbol", "direction"),
		Run: func(ctx context.Context, _, _, args string) (coderuntime.ToolResult, error) {
			var a struct {
				symbolArgs
				Direction string `json:"direction"`
			}
			if r, bad := bind(args, &a); bad {
				return r, nil
			}
			return ts.hierarchy(ctx, a.symbolArgs, a.Direction), nil
		},
	}
}

func (ts *toolset) hierarchy(ctx context.Context, a symbolArgs, direction string) coderuntime.ToolResult {
	if a.Symbol == "" {
		return refuse("symbol is required.")
	}
	if direction != "incoming" && direction != "outgoing" {
		return refuse(`direction must be "incoming" or "outgoing".`)
	}
	st, err := ts.bestSite(ctx, a.Symbol, ts.searchFrom(a.Path))
	if err != nil {
		return refuse(explainLookup(err, a.Symbol))
	}
	items, err := st.sess.hierarchyItems(ctx, st.path, st.line, st.col)
	switch {
	case err != nil:
		return refuse(fmt.Sprintf("Could not prepare the call hierarchy: %v", err))
	case len(items) == 0:
		return refuse(fmt.Sprintf("The server has no call hierarchy for %q.", a.Symbol))
	}
	item := items[0]

	type edge struct {
		uri  protocol.DocumentURI
		at   protocol.Range
		name string
	}
	var edges []edge
	label := "Callers of"
	if direction == "incoming" {
		calls, err := st.sess.callers(ctx, item)
		if err != nil {
			return refuse(fmt.Sprintf("Could not fetch the callers: %v", err))
		}
		for _, c := range calls {
			edges = append(edges, edge{c.From.URI, c.From.Range, c.From.Name})
		}
	} else {
		label = "Calls made by"
		calls, err := st.sess.callees(ctx, item)
		if err != nil {
			return refuse(fmt.Sprintf("Could not fetch the callees: %v", err))
		}
		for _, c := range calls {
			edges = append(edges, edge{c.To.URI, c.To.Range, c.To.Name})
		}
	}

	var b strings.Builder
	if len(edges) == 0 {
		fmt.Fprintf(&b, "%s %s: none found.\n", label, item.Name)
		return reply(b.String())
	}
	fmt.Fprintf(&b, "%s %s (%d):\n", label, item.Name, len(edges))
	for _, e := range edges {
		p, _ := e.uri.Path()
		fmt.Fprintf(&b, "  %s:%d  %s\n", p, e.at.Start.Line+1, e.name)
	}
	return reply(b.String())
}
