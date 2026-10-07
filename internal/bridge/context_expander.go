package bridge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var mentionRegex = regexp.MustCompile(`@([a-zA-Z0-9_\-\.\/]+)`)

// expandUserMentions analyzes user prompt for @mentions (like @Git, @Terminal, @Codebase, @filepath)
// and appends authoritative context snapshots to provide Cursor Composer-like target awareness.
func (s *AppService) expandUserMentions(ctx context.Context, userText string) string {
	if !strings.Contains(userText, "@") {
		return userText
	}

	matches := mentionRegex.FindAllStringSubmatch(userText, -1)
	if len(matches) == 0 {
		return userText
	}

	var contextBlocks []string
	seenMentions := make(map[string]bool)
	wsRoot := s.rt.WorkspaceRoot()

	for _, m := range matches {
		raw := m[0]
		target := m[1]
		if seenMentions[strings.ToLower(target)] {
			continue
		}
		seenMentions[strings.ToLower(target)] = true

		switch strings.ToLower(target) {
		case "git":
			gitCtx := s.collectGitContext(wsRoot)
			if gitCtx != "" {
				contextBlocks = append(contextBlocks, gitCtx)
			}
		case "terminal":
			termCtx := s.collectTerminalContext()
			if termCtx != "" {
				contextBlocks = append(contextBlocks, termCtx)
			}
		case "codebase":
			codebaseCtx := s.collectCodebaseContext(wsRoot)
			if codebaseCtx != "" {
				contextBlocks = append(contextBlocks, codebaseCtx)
			}
		case "problems":
			problemsCtx := s.collectProblemsContext(wsRoot)
			if problemsCtx != "" {
				contextBlocks = append(contextBlocks, problemsCtx)
			}
		default:
			// Check if target is a file or directory in workspace
			fileCtx := s.collectFileContext(wsRoot, target)
			if fileCtx != "" {
				contextBlocks = append(contextBlocks, fileCtx)
			}
		}
		_ = raw
	}

	if len(contextBlocks) == 0 {
		return userText
	}

	var sb strings.Builder
	sb.WriteString(userText)
	sb.WriteString("\n\n---\n### [Auto-Injected Context via @ Mentions]\n")
	for _, block := range contextBlocks {
		sb.WriteString(block)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

func (s *AppService) collectGitContext(root string) string {
	if root == "" {
		return ""
	}
	st, err := runGit(root, "status", "--short", "-b")
	if err != nil {
		return ""
	}
	diff, _ := runGit(root, "diff", "--no-color", "HEAD")
	if len(diff) > 4000 {
		diff = diff[:4000] + "\n... [diff truncated]"
	}

	var sb strings.Builder
	sb.WriteString("#### 🌿 Context: Git Status & Diff\n```\n")
	sb.WriteString(strings.TrimSpace(st))
	sb.WriteString("\n```\n")
	if strings.TrimSpace(diff) != "" {
		sb.WriteString("```diff\n")
		sb.WriteString(strings.TrimSpace(diff))
		sb.WriteString("\n```")
	}
	return sb.String()
}

func (s *AppService) collectTerminalContext() string {
	ts := s.rt.TerminalService()
	if ts == nil {
		return ""
	}
	terms := ts.ListTerminals()
	if len(terms) == 0 {
		return ""
	}
	// Pick the latest terminal
	active := terms[len(terms)-1]
	buf := ts.PollTerminal(active.ID)
	if buf == "" {
		return fmt.Sprintf("#### 📟 Context: Terminal (%s)\n[Terminal is idle with no recent unread output]", active.Title)
	}
	if len(buf) > 3000 {
		buf = buf[len(buf)-3000:]
	}
	return fmt.Sprintf("#### 📟 Context: Terminal Output (%s)\n```\n%s\n```", active.Title, strings.TrimSpace(buf))
}

func (s *AppService) collectCodebaseContext(root string) string {
	if root == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("#### ⚡ Context: Codebase Overview (%s)\n", filepath.Base(root)))

	// Check for README or entry points
	readmeCandidates := []string{"README.md", "readme.md", "README", "package.json", "go.mod", "Cargo.toml"}
	for _, cand := range readmeCandidates {
		p := filepath.Join(root, cand)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			if data, rErr := os.ReadFile(p); rErr == nil {
				txt := string(data)
				if len(txt) > 2000 {
					txt = txt[:2000] + "\n... [truncated]"
				}
				sb.WriteString(fmt.Sprintf("File `%s`:\n```\n%s\n```\n", cand, strings.TrimSpace(txt)))
				break
			}
		}
	}

	// List top-level entries
	entries, err := os.ReadDir(root)
	if err == nil {
		var names []string
		for _, e := range entries {
			n := e.Name()
			if strings.HasPrefix(n, ".") || n == "node_modules" || n == "dist" || n == "bin" {
				continue
			}
			if e.IsDir() {
				names = append(names, n+"/")
			} else {
				names = append(names, n)
			}
		}
		sb.WriteString("Top-level directory structure:\n- " + strings.Join(names, "\n- "))
	}
	return sb.String()
}

func (s *AppService) collectProblemsContext(root string) string {
	tb := s.getCodetools()
	if tb == nil {
		return "#### ⚠️ Context: Diagnostics\n[Language servers not initialized in active workspace]"
	}
	states := tb.LSPStates()
	if len(states) == 0 {
		return "#### ⚠️ Context: Diagnostics\n[No active language servers detected]"
	}

	var sb strings.Builder
	sb.WriteString("#### ⚠️ Context: LSP Diagnostics\n")
	hasDiags := false
	for name, st := range states {
		if st.DiagnosticCount > 0 {
			hasDiags = true
			diags := tb.LSPDiagnostics(name)
			sb.WriteString(fmt.Sprintf("Server `%s` reported %d issue(s):\n```json\n%v\n```\n", name, st.DiagnosticCount, diags))
		}
	}
	if !hasDiags {
		sb.WriteString("[All active language servers report 0 errors - workspace syntax is clean]\n")
	}
	return sb.String()
}

func (s *AppService) collectFileContext(root, relPath string) string {
	if root == "" || relPath == "" {
		return ""
	}
	clean := filepath.Clean(relPath)
	full := filepath.Join(root, clean)

	// Ensure path stays within root
	if !strings.HasPrefix(filepath.Clean(full), filepath.Clean(root)) {
		return ""
	}

	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return ""
	}

	// Read content up to 32KB
	data, err := os.ReadFile(full)
	if err != nil {
		return ""
	}
	txt := string(data)
	if len(txt) > 32*1024 {
		txt = txt[:32*1024] + "\n... [file truncated, exceeds 32KB]"
	}

	return fmt.Sprintf("#### 📄 Context File: `%s`\n```%s\n%s\n```",
		filepath.ToSlash(clean),
		strings.TrimPrefix(filepath.Ext(clean), "."),
		txt,
	)
}
