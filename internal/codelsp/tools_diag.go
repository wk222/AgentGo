package codelsp

// lsp_diagnostics: the problems the language servers currently report.

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"

	"agentgo/internal/coderuntime"
)

const (
	listedPerSection = 10
	settleBudget     = 5 * time.Second
)

func (ts *toolset) diagnosticsTool() coderuntime.Tool {
	return coderuntime.Tool{
		Name: "lsp_diagnostics",
		Description: "Report the errors, warnings and hints the language servers currently see: for one file, or for the whole project when file_path is left empty. " +
			"Errors come first. Empty output only means no server reported anything, which does not prove the code builds.",
		Schema: schemaOf(map[string]any{
			"file_path": textProp("File to check; leave empty for the whole project"),
		}),
		Run: func(ctx context.Context, _, _, args string) (coderuntime.ToolResult, error) {
			var a struct {
				FilePath string `json:"file_path"`
			}
			if r, bad := bind(args, &a); bad {
				return r, nil
			}
			ts.settle(ctx, a.FilePath)
			return reply(ts.report(a.FilePath)), nil
		},
	}
}

// settle makes the servers re-read what is on disk and waits for them to finish
// reporting, so the answer reflects the file as it is now. An empty path does
// this for every open document of every server.
func (ts *toolset) settle(ctx context.Context, file string) {
	var wg sync.WaitGroup
	if file == "" {
		for _, s := range ts.sv.sessions() {
			wg.Go(func() {
				s.refreshAll(ctx)
				if err := s.nudgeWorkspace(ctx); err != nil {
					slog.Warn("lsp: workspace nudge failed", "server", s.name, "error", err)
				}
				s.awaitDiagnostics(ctx, settleBudget)
			})
		}
		wg.Wait()
		return
	}
	file = ts.absPath(file)
	ts.sv.ensure(ctx, file)
	for _, s := range ts.sv.sessions() {
		if !s.serves(file) {
			continue
		}
		_ = s.open(ctx, file)
		_ = s.refresh(ctx, file)
		wg.Go(func() { s.awaitDiagnostics(ctx, settleBudget) })
	}
	wg.Wait()
}

// problem is one diagnostic ready to print.
type problem struct {
	severity protocol.DiagnosticSeverity
	path     string
	line     int
	col      int
	text     string
}

func (p problem) isError() bool   { return p.severity == protocol.SeverityError }
func (p problem) isWarning() bool { return p.severity == protocol.SeverityWarning }

func severityWord(s protocol.DiagnosticSeverity) string {
	switch s {
	case protocol.SeverityError:
		return "error"
	case protocol.SeverityWarning:
		return "warning"
	case protocol.SeverityHint:
		return "hint"
	}
	return "info"
}

func newProblem(path, server string, d protocol.Diagnostic) problem {
	origin := server
	if d.Source != "" {
		origin += " " + d.Source
	}
	code := ""
	if d.Code != nil {
		code = fmt.Sprintf(" [%v]", d.Code)
	}
	var tags []string
	for _, t := range d.Tags {
		switch t {
		case protocol.Unnecessary:
			tags = append(tags, "unnecessary")
		case protocol.Deprecated:
			tags = append(tags, "deprecated")
		}
	}
	tagText := ""
	if len(tags) > 0 {
		tagText = " (" + strings.Join(tags, ", ") + ")"
	}
	sev := d.Severity
	if sev == 0 { // the server left it unspecified
		sev = protocol.SeverityInformation
	}
	return problem{
		severity: sev, path: path,
		line: int(d.Range.Start.Line) + 1, col: int(d.Range.Start.Character) + 1,
		text: fmt.Sprintf("%s%s%s: %s", origin, code, tagText, strings.TrimSpace(d.Message)),
	}
}

// collect splits everything the servers published into the problems in file and
// the rest, each ordered errors first, then by place.
func (ts *toolset) collect(file string) (inFile, elsewhere []problem) {
	for _, s := range ts.sv.sessions() {
		for uri, list := range s.diags.snapshot() {
			p, err := uri.Path()
			if err != nil {
				continue
			}
			for _, d := range list {
				pr := newProblem(p, s.name, d)
				if file != "" && p == file {
					inFile = append(inFile, pr)
				} else {
					elsewhere = append(elsewhere, pr)
				}
			}
		}
	}
	order := func(a, b problem) int {
		return cmp.Or(cmp.Compare(a.severity, b.severity), cmp.Compare(a.path, b.path), cmp.Compare(a.line, b.line), cmp.Compare(a.col, b.col))
	}
	slices.SortFunc(inFile, order)
	slices.SortFunc(elsewhere, order)
	return inFile, elsewhere
}

func tally(ps []problem) string {
	e, w := 0, 0
	for _, p := range ps {
		switch {
		case p.isError():
			e++
		case p.isWarning():
			w++
		}
	}
	return fmt.Sprintf("%s, %s", plural(e, "error", "errors"), plural(w, "warning", "warnings"))
}

// report renders the diagnostics; it is empty when no server reported anything.
func (ts *toolset) report(file string) string {
	if file != "" {
		file = ts.absPath(file)
	}
	inFile, elsewhere := ts.collect(file)
	var b strings.Builder
	section := func(title string, ps []problem, withPath bool) {
		if len(ps) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s (%s):\n", title, tally(ps))
		for _, p := range ps[:min(len(ps), listedPerSection)] {
			where := fmt.Sprintf("%d:%d", p.line, p.col)
			if withPath {
				where = fmt.Sprintf("%s:%s", p.path, where)
			}
			fmt.Fprintf(&b, "  %-7s %s %s\n", severityWord(p.severity), where, p.text)
		}
		if extra := len(ps) - listedPerSection; extra > 0 {
			fmt.Fprintf(&b, "  ... and %d more not shown\n", extra)
		}
		b.WriteByte('\n')
	}
	section("Problems in "+file, inFile, false)
	if file == "" {
		section("Problems in the project", elsewhere, true)
	} else {
		section("Problems in other files", elsewhere, true)
	}
	return strings.TrimRight(b.String(), "\n")
}
