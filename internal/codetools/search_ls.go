package codetools

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
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
		Description: fmt.Sprintf("List files and directories as a tree; skips hidden files and common system dirs; max %d files. "+
			"Use glob to find files by pattern, grep to search contents.", maxLSFiles),
		Schema: objSchema(map[string]any{
			"path":   prop("string", "The path to the directory to list (defaults to current working directory)"),
			"ignore": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "List of glob patterns to ignore"},
			"depth":  prop("integer", "The maximum depth to traverse"),
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
	path := expandPath(cmp.Or(a.Path, s.WorkDir))
	if !filepath.IsAbs(path) && !(runtime.GOOS == "windows" && strings.HasPrefix(filepath.ToSlash(path), "/")) {
		path = filepath.Join(s.WorkDir, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return toolErr(fmt.Sprintf("error resolving search path: %v", err))
	}
	root, _ := filepath.Abs(s.WorkDir)
	if rel, err := filepath.Rel(root, abs); err != nil || strings.HasPrefix(rel, "..") {
		if s.Approve != nil && !s.Approve(ctx, coderuntime.FSRequest{SessionID: sessionID, CallID: callID, Tool: "ls", Action: "list",
			Path: abs, Description: "List directory outside working directory: " + abs}) {
			return toolErr("User denied permission")
		}
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return toolErr("path does not exist: " + path)
	}
	files, truncated := listDirectory(ctx, path, a.Ignore, a.Depth, maxLSFiles)

	var out string
	if truncated {
		out = fmt.Sprintf("There are more than %d files in the directory. Use a more specific path or use the Glob tool to find specific files. The first %[1]d files and directories are included below.\n", maxLSFiles)
	}
	if a.Depth > 0 {
		out = fmt.Sprintf("The directory tree is shown up to a depth of %d. Use a higher depth and a specific path to see more levels.\n", a.Depth)
	}
	return toolOK(out + "\n" + printTree(buildTree(files, path), path))
}

// listDirectory returns the entries below root (directories end in a path
// separator), skipping ignored ones, and whether the limit cut it short.
func listDirectory(ctx context.Context, root string, extraIgnore []string, depth, limit int) ([]string, bool) {
	ig := newIgnorer(root)
	var found []string
	truncated := false
	walkTree(ctx, root, walkOpts{FilesFirst: true, Follow: true, MaxDepth: depth}, func(path string, isDir bool) walkAction {
		if ig.ignored(path, isDir, extraIgnore) {
			if isDir {
				return walkSkipDir
			}
			return walkContinue
		}
		if path != root {
			if isDir {
				path += string(filepath.Separator)
			}
			found = append(found, path)
		}
		if limit > 0 && len(found) >= limit {
			truncated = true
			return walkStop
		}
		return walkContinue
	})
	return found, truncated
}

type treeNode struct {
	name     string
	dir      bool
	children []*treeNode
}

// buildTree folds the flat path list into a tree, creating directory nodes for
// every parent.
func buildTree(paths []string, root string) []*treeNode {
	var top []*treeNode
	byPath := map[string]*treeNode{}
	root = filepath.ToSlash(root)
	for _, p := range paths {
		rel := strings.TrimPrefix(filepath.ToSlash(p), root)
		var parts []string
		for _, part := range strings.Split(rel, "/") {
			if part != "" {
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			continue
		}
		cur, parent := "", ""
		for i, part := range parts {
			cur = path.Join(cur, part)
			if _, ok := byPath[cur]; ok {
				parent = cur
				continue
			}
			last := i == len(parts)-1
			n := &treeNode{name: part, dir: !last || strings.HasSuffix(rel, "/")}
			byPath[cur] = n
			if i > 0 && parent != "" {
				if pn, ok := byPath[parent]; ok {
					pn.children = append(pn.children, n)
				}
			} else {
				top = append(top, n)
			}
			parent = cur
		}
	}
	return top
}

func printTree(tree []*treeNode, root string) string {
	var b strings.Builder
	b.WriteString("- ")
	b.WriteString(filepath.ToSlash(root))
	if root[len(root)-1] != '/' {
		b.WriteByte('/')
	}
	b.WriteByte('\n')
	for _, n := range tree {
		printNode(&b, n, 1)
	}
	return b.String()
}

func printNode(b *strings.Builder, n *treeNode, level int) {
	name := n.name
	if n.dir {
		name += "/"
	}
	fmt.Fprintf(b, "%s- %s\n", strings.Repeat("  ", level), name)
	for _, c := range n.children {
		printNode(b, c, level+1)
	}
}
