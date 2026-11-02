package codetools

// ls: show a folder as an indented tree.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agentgo/internal/coderuntime"
)

type lsArgs struct {
	Path   string   `json:"path"`
	Ignore []string `json:"ignore"`
	Depth  int      `json:"depth"`
}

func (s *searcher) lsTool() coderuntime.Tool {
	return coderuntime.Tool{
		Name: "ls",
		Description: fmt.Sprintf("Show a folder's files and sub-folders as an indented tree (folders end in a slash, files come before sub-folders). "+
			"Dotfiles are shown; ignored folders (.git, node_modules, ...) and build output are left out, and a listing stops after %d entries. "+
			"To find files by name use glob; to search inside them use grep.", lsLimit),
		Schema: objSchema(map[string]any{
			"path":   prop("string", "Folder to show; defaults to the working directory"),
			"ignore": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Glob patterns for names to leave out"},
			"depth":  prop("integer", "How many levels below the folder to show (default: all)"),
		}),
		Parallel: true,
		Run: func(ctx context.Context, sessionID, callID, argsJSON string) (coderuntime.ToolResult, error) {
			var a lsArgs
			if r, bad := decode(argsJSON, &a); bad {
				return r, nil
			}
			return s.ls(ctx, sessionID, callID, a), nil
		},
	}
}

// expandPath resolves "~" and environment variables in a model-supplied path.
func expandPath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[1:])
		}
	}
	return os.ExpandEnv(p)
}

func (s *searcher) ls(ctx context.Context, sessionID, callID string, a lsArgs) coderuntime.ToolResult {
	root := s.resolveRoot(a.Path)
	if msg, ok := s.allowRoot(ctx, sessionID, callID, "ls", "list", root); !ok {
		return toolErr(msg)
	}
	info, err := os.Stat(root)
	switch {
	case os.IsNotExist(err):
		return toolErr("No such folder: " + root)
	case err != nil:
		return toolErr(fmt.Sprintf("Cannot read %s: %v", root, err))
	case !info.IsDir():
		return toolErr(root + " is a file, not a folder. Use view to read it.")
	}

	var tree strings.Builder
	entries := 0
	capped := false
	skip := newIgnorer(root)
	walkTree(ctx, root, walkOpts{FilesFirst: true, Follow: true, MaxDepth: a.Depth}, func(p string, isDir bool) walkAction {
		if p == root {
			return walkContinue
		}
		if skip.ignored(p, isDir, a.Ignore) {
			if isDir {
				return walkSkipDir
			}
			return walkContinue
		}
		if entries >= lsLimit {
			capped = true
			return walkStop
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return walkContinue
		}
		level := strings.Count(filepath.ToSlash(rel), "/") + 1
		name := filepath.Base(p)
		if isDir {
			name += "/"
		}
		tree.WriteString(strings.Repeat("  ", level))
		tree.WriteString(name)
		tree.WriteByte('\n')
		entries++
		return walkContinue
	})

	var out strings.Builder
	if a.Depth > 0 {
		fmt.Fprintf(&out, "(Showing %s below this folder.)\n", plural(a.Depth, "level", "levels"))
	}
	if capped {
		fmt.Fprintf(&out, "(The listing stopped after %d entries. Pass a deeper `path`, or use glob to find specific files.)\n", lsLimit)
	}
	out.WriteString(strings.TrimSuffix(filepath.ToSlash(root), "/") + "/\n")
	if entries == 0 {
		out.WriteString("  (nothing to show)\n")
	}
	out.WriteString(tree.String())
	return toolOK(out.String())
}
