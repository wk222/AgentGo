package tui

import (
	"encoding/json"
	"fmt"
	"strings"
)

const previewMaxLines = 24

// preview describes what an approval is about to do: file-changing tools show
// the change as a diff, a command shows the command, anything else the
// arguments.
func preview(tool, args string) string {
	name := strings.TrimPrefix(tool, "code_")
	var a struct {
		FilePath string `json:"file_path"`
		Old      string `json:"old_string"`
		New      string `json:"new_string"`
		Content  string `json:"content"`
		Command  string `json:"command"`
		Edits    []struct {
			Old string `json:"old_string"`
			New string `json:"new_string"`
		} `json:"edits"`
	}
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return capLines(args, previewMaxLines)
	}
	var b strings.Builder
	switch {
	case name == "edit" && a.FilePath != "":
		fmt.Fprintf(&b, "%s\n", a.FilePath)
		writeChange(&b, a.Old, a.New)
	case name == "multiedit" && a.FilePath != "":
		fmt.Fprintf(&b, "%s  (%d edits)\n", a.FilePath, len(a.Edits))
		for i, e := range a.Edits {
			if i > 0 {
				b.WriteString("@@\n")
			}
			writeChange(&b, e.Old, e.New)
		}
	case name == "write" && a.FilePath != "":
		fmt.Fprintf(&b, "%s  (new content)\n", a.FilePath)
		writeChange(&b, "", a.Content)
	case a.Command != "":
		b.WriteString("$ " + a.Command)
	default:
		b.WriteString(prettyJSON(args))
	}
	return capLines(strings.TrimRight(b.String(), "\n"), previewMaxLines)
}

func writeChange(b *strings.Builder, oldS, newS string) {
	for _, l := range splitLines(oldS) {
		b.WriteString("- " + l + "\n")
	}
	for _, l := range splitLines(newS) {
		b.WriteString("+ " + l + "\n")
	}
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// capLines keeps the first n lines of s and says how many were cut.
func capLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n… %d more lines", len(lines)-n)
}

// diagnostics pulls the problem lines out of an lsp_diagnostics result and
// counts them by severity. ok is false when the text carries no diagnostics
// block.
func diagnostics(result string) (lines []string, errs, warns int, ok bool) {
	for _, tag := range []string{"file_diagnostics", "project_diagnostics"} {
		open, closeTag := "<"+tag+">", "</"+tag+">"
		i := strings.Index(result, open)
		if i < 0 {
			continue
		}
		body := result[i+len(open):]
		if j := strings.Index(body, closeTag); j >= 0 {
			body = body[:j]
		}
		ok = true
		for _, l := range strings.Split(body, "\n") {
			l = strings.TrimSpace(l)
			switch {
			case strings.HasPrefix(l, "Error:"), strings.HasPrefix(l, "error:"):
				errs++
				lines = append(lines, l)
			case strings.HasPrefix(l, "Warn"), strings.HasPrefix(l, "warn"):
				warns++
				lines = append(lines, l)
			}
		}
	}
	return lines, errs, warns, ok
}
