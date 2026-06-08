package workspace

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// CopyFile copies a single file from src to dst.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// CopyDirExcluding recursively copies src directory to dst, excluding names specified in exclusions.
func CopyDirExcluding(src, dst string, exclude []string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		for _, ex := range exclude {
			if rel == ex || strings.HasPrefix(rel, ex+string(filepath.Separator)) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}

		return CopyFile(path, target)
	})
}

// CreateSandboxWorkspace creates a physical temporary clone workspace, excluding bulky caches.
func CreateSandboxWorkspace(realRootDir string) (string, error) {
	// Standard relative tmp path within the execution directory
	tmpRoot := filepath.Join(".local", "tmp", "workspace-"+uuid.NewString())
	if err := os.MkdirAll(tmpRoot, 0755); err != nil {
		return "", err
	}

	excludes := []string{"node_modules", ".git", ".local", "attached_assets", "dist"}
	if err := CopyDirExcluding(realRootDir, tmpRoot, excludes); err != nil {
		_ = os.RemoveAll(tmpRoot)
		return "", fmt.Errorf("failed creating sandbox workspace clone: %w", err)
	}

	return tmpRoot, nil
}

// SyncSandboxToReal synchronizes verified static build artifacts back to the real repository.
func SyncSandboxToReal(sandboxDir, realRootDir string, subPath string) error {
	src := filepath.Join(sandboxDir, subPath)
	dst := filepath.Join(realRootDir, subPath)

	if _, err := os.Stat(src); os.IsNotExist(err) {
		return fmt.Errorf("specified build path %s does not exist in sandbox", subPath)
	}

	return CopyDirExcluding(src, dst, nil)
}

// CleanupSandboxWorkspace purges the temporary sandbox workspace cleanly.
func CleanupSandboxWorkspace(sandboxDir string) error {
	// Verify that we are deleting within the local tmp directory boundaries to prevent wild cleanups
	cleanDir := filepath.Clean(sandboxDir)
	boundary := filepath.Clean(filepath.Join(".local", "tmp"))
	if !strings.Contains(cleanDir, boundary) {
		return fmt.Errorf("denied cleanup: path outside temporary boundaries: %s", sandboxDir)
	}
	return os.RemoveAll(sandboxDir)
}
