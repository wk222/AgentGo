package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/plantask"
)

// diskPlantaskBackend implements plantask.Backend for persistent task lists in the workspace or dataDir.
type diskPlantaskBackend struct {
	baseDir string
}

// newDiskPlantaskBackend creates a plantask storage backend anchored at baseDir.
func newDiskPlantaskBackend(baseDir string) plantask.Backend {
	cleanDir := filepath.Clean(baseDir)
	_ = os.MkdirAll(cleanDir, 0o755)
	return &diskPlantaskBackend{baseDir: cleanDir}
}

func (b *diskPlantaskBackend) resolvePath(p string) (string, error) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(b.baseDir, p)
	}
	clean := filepath.Clean(p)
	if clean != b.baseDir && !strings.HasPrefix(clean, b.baseDir+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %s escapes base directory %s", p, b.baseDir)
	}
	return clean, nil
}

func (b *diskPlantaskBackend) LsInfo(ctx context.Context, req *plantask.LsInfoRequest) ([]plantask.FileInfo, error) {
	targetDir := b.baseDir
	if req != nil && req.Path != "" {
		resolved, err := b.resolvePath(req.Path)
		if err != nil {
			return nil, err
		}
		targetDir = resolved
	}

	entries, err := os.ReadDir(targetDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []plantask.FileInfo{}, nil
		}
		return nil, err
	}

	var results []plantask.FileInfo
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		results = append(results, plantask.FileInfo{
			Path:       filepath.Join(targetDir, entry.Name()),
			IsDir:      entry.IsDir(),
			Size:       info.Size(),
			ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	return results, nil
}

func (b *diskPlantaskBackend) Read(ctx context.Context, req *plantask.ReadRequest) (*filesystem.FileContent, error) {
	if req == nil || req.FilePath == "" {
		return nil, os.ErrInvalid
	}
	target, err := b.resolvePath(req.FilePath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}

	content := string(data)
	if req.Offset > 1 || req.Limit > 0 {
		lines := strings.Split(content, "\n")
		start := req.Offset - 1
		if start < 0 {
			start = 0
		}
		if start >= len(lines) {
			return &filesystem.FileContent{Content: ""}, nil
		}
		end := len(lines)
		if req.Limit > 0 && start+req.Limit < end {
			end = start + req.Limit
		}
		content = strings.Join(lines[start:end], "\n")
	}

	return &filesystem.FileContent{Content: content}, nil
}

func (b *diskPlantaskBackend) Write(ctx context.Context, req *plantask.WriteRequest) error {
	if req == nil || req.FilePath == "" {
		return os.ErrInvalid
	}
	target, err := b.resolvePath(req.FilePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(req.Content), 0o644)
}

func (b *diskPlantaskBackend) Delete(ctx context.Context, req *plantask.DeleteRequest) error {
	if req == nil || req.FilePath == "" {
		return os.ErrInvalid
	}
	target, err := b.resolvePath(req.FilePath)
	if err != nil {
		return err
	}
	err = os.Remove(target)
	if err != nil && !errorsIsNotExist(err) {
		return err
	}
	return nil
}

func errorsIsNotExist(err error) bool {
	return os.IsNotExist(err) || (err != nil && strings.Contains(err.Error(), "cannot find the file"))
}
