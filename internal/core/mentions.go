package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"agentgo/internal/workspace"
)

// MentionSources are the live things an @mention can pull in. Root is the
// workspace; Terminal and Diagnostics may be nil when the host has none.
type MentionSources struct {
	Root string
	// Terminal reports the most recent terminal and its unread output.
	Terminal func() (title, unread string, ok bool)
	// Diagnostics reports issue counts per language server and a way to read the
	// details of one; ok is false when no language servers are set up.
	Diagnostics func() (counts map[string]int, detail func(name string) any, ok bool)
}

var mentionRegex = regexp.MustCompile(`@([a-zA-Z0-9_\-\.\/]+)`)

// ExpandMentions analyzes user prompt for @mentions (like @Git, @Terminal, @Codebase, @filepath)
// and appends authoritative context snapshots to provide Cursor Composer-like target awareness.
func ExpandMentions(userText string, src MentionSources) string {
	if !strings.Contains(userText, "@") {
		return userText
	}

	matches := mentionRegex.FindAllStringSubmatch(userText, -1)
	if len(matches) == 0 {
		return userText
	}

	var contextBlocks []string
	seenMentions := make(map[string]bool)
	wsRoot := src.Root

	for _, m := range matches {
		raw := m[0]
		target := m[1]
		if seenMentions[strings.ToLower(target)] {
			continue
		}
		seenMentions[strings.ToLower(target)] = true

		switch strings.ToLower(target) {
		case "git":
			gitCtx := collectGitContext(wsRoot)
			if gitCtx != "" {
				contextBlocks = append(contextBlocks, gitCtx)
			}
		case "terminal":
			termCtx := collectTerminalContext(src.Terminal)
			if termCtx != "" {
				contextBlocks = append(contextBlocks, termCtx)
			}
		case "codebase":
			codebaseCtx := collectCodebaseContext(wsRoot)
			if codebaseCtx != "" {
				contextBlocks = append(contextBlocks, codebaseCtx)
			}
		case "problems":
			problemsCtx := collectProblemsContext(src.Diagnostics)
			if problemsCtx != "" {
				contextBlocks = append(contextBlocks, problemsCtx)
			}
		default:
			// Check if target is a file or directory in workspace
			fileCtx := collectFileContext(wsRoot, target)
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

func collectGitContext(root string) string {
	if root == "" {
		return ""
	}
	st, err := workspace.RunGit(root, "status", "--short", "-b")
	if err != nil {
		return ""
	}
	diff, _ := workspace.RunGit(root, "diff", "--no-color", "HEAD")
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

func collectTerminalContext(latest func() (title, unread string, ok bool)) string {
	if latest == nil {
		return ""
	}
	title, buf, ok := latest()
	if !ok {
		return ""
	}
	if buf == "" {
		return fmt.Sprintf("#### 📟 Context: Terminal (%s)\n[Terminal is idle with no recent unread output]", title)
	}
	if len(buf) > 3000 {
		buf = buf[len(buf)-3000:]
	}
	return fmt.Sprintf("#### 📟 Context: Terminal Output (%s)\n```\n%s\n```", title, strings.TrimSpace(buf))
}

func collectCodebaseContext(root string) string {
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

func collectProblemsContext(diag func() (counts map[string]int, detail func(name string) any, ok bool)) string {
	if diag == nil {
		return "#### ⚠️ Context: Diagnostics\n[Language servers not initialized in active workspace]"
	}
	counts, detail, ok := diag()
	if !ok {
		return "#### ⚠️ Context: Diagnostics\n[Language servers not initialized in active workspace]"
	}
	if len(counts) == 0 {
		return "#### ⚠️ Context: Diagnostics\n[No active language servers detected]"
	}

	var sb strings.Builder
	sb.WriteString("#### ⚠️ Context: LSP Diagnostics\n")
	hasDiags := false
	for name, n := range counts {
		if n > 0 {
			hasDiags = true
			sb.WriteString(fmt.Sprintf("Server `%s` reported %d issue(s):\n```json\n%v\n```\n", name, n, detail(name)))
		}
	}
	if !hasDiags {
		sb.WriteString("[All active language servers report 0 errors - workspace syntax is clean]\n")
	}
	return sb.String()
}

func collectFileContext(root, relPath string) string {
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
