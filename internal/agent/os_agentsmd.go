package agent

import (
	"context"
	"os"
	"path/filepath"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/agentsmd"
)

// osAgentsMDBackend implements agentsmd.Backend without eino-ext/local (no go-fitz).
type osAgentsMDBackend struct {
	root string
}

func newOSAgentsMDBackend(workspaceRoot string) agentsmd.Backend {
	return &osAgentsMDBackend{root: workspaceRoot}
}

func (b *osAgentsMDBackend) Read(_ context.Context, req *agentsmd.ReadRequest) (*agentsmd.FileContent, error) {
	p := req.FilePath
	if !filepath.IsAbs(p) {
		p = filepath.Join(b.root, p)
	}
	p = filepath.Clean(p)
	if !stringsHasPrefix(p, filepath.Clean(b.root)) {
		return nil, os.ErrPermission
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	fc := filesystem.FileContent{Content: string(data)}
	return (*agentsmd.FileContent)(&fc), nil
}

func stringsHasPrefix(path, root string) bool {
	if root == "" {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !filepath.IsAbs(rel) && rel != ""
}

func agentsMDPaths(workspaceRoot string) []string {
	var paths []string
	for _, name := range []string{"AGENTS.md", "agents.md", "SOUL.md", "TEAM.md"} {
		p := filepath.Join(workspaceRoot, name)
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
	}
	rules := filepath.Join(workspaceRoot, ".cursor", "rules")
	_ = filepath.WalkDir(rules, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".mdc" || filepath.Ext(path) == ".md" {
			paths = append(paths, path)
		}
		return nil
	})
	return paths
}
