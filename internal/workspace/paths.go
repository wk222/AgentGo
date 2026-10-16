package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// MaxPreviewBytes is the largest file the UI previews as text.
const MaxPreviewBytes int64 = 512 * 1024

// RunGit executes a git subcommand inside dir with an 8s timeout, returning
// stdout. On failure it returns stderr (or the exec error) wrapped.
func RunGit(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	HideExecWindow(cmd)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

func CleanRel(relPath string, allowRoot bool) (string, error) {
	clean := filepath.ToSlash(filepath.Clean("/" + strings.ReplaceAll(relPath, "\\", "/")))
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." {
		if allowRoot {
			return "", nil
		}
		return "", fmt.Errorf("invalid path: %q", relPath)
	}
	if strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("invalid path: %q", relPath)
	}
	return clean, nil
}

func FullPath(root, relPath string, allowRoot bool) (string, string, error) {
	clean, err := CleanRel(relPath, allowRoot)
	if err != nil {
		return "", "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	fullAbs, err := filepath.Abs(filepath.Join(rootAbs, filepath.FromSlash(clean)))
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(rootAbs, fullAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		if err == nil {
			err = fmt.Errorf("invalid path: %q", relPath)
		}
		return "", "", err
	}
	return clean, fullAbs, nil
}
