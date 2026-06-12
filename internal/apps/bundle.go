package apps

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	AppMetadataFile = "app.json"
	AppEntryFile    = "index.html"
)

// ResolveBundleDir returns the on-disk directory for an app bundle.
func ResolveBundleDir(appsRoot, workspaceRoot string, app InnerApp) (string, bool) {
	roots := []string{appsRoot, workspaceRoot}
	if p := strings.TrimSpace(app.BundlePath); p != "" {
		if filepath.IsAbs(p) {
			for _, root := range roots {
				if bundleDirIsUnderRoot(root, p) {
					if st, err := os.Stat(p); err == nil && st.IsDir() {
						return p, true
					}
				}
			}
		} else {
			for _, root := range roots {
				if full, ok := resolveBundleDirUnderRoot(root, p); ok {
					return full, true
				}
			}
		}
	}
	for _, root := range roots {
		if full, ok := resolveBundleDirUnderRoot(root, app.Name); ok {
			return full, true
		}
	}
	return "", false
}

// ReadBundleFile reads a file relative to the app bundle (safe path).
func ReadBundleFile(dir, rel string) ([]byte, string, error) {
	full, clean, err := safeBundleFilePath(dir, rel)
	if err != nil {
		return nil, "", err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, "", err
	}
	return b, mimeForPath(clean), nil
}

func resolveBundleDirUnderRoot(root, rel string) (string, bool) {
	root = strings.TrimSpace(root)
	rel = strings.TrimSpace(rel)
	if root == "" || rel == "" || filepath.IsAbs(rel) {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", false
	}
	full := filepath.Join(root, clean)
	if !bundleDirIsUnderRoot(root, full) {
		return "", false
	}
	if st, err := os.Stat(full); err == nil && st.IsDir() {
		return full, true
	}
	return "", false
}

func bundleDirIsUnderRoot(root, dir string) bool {
	root = strings.TrimSpace(root)
	dir = strings.TrimSpace(dir)
	if root == "" || dir == "" {
		return false
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err == nil {
		rootAbs = rootReal
	}
	dirReal, err := filepath.EvalSymlinks(dirAbs)
	if err == nil {
		dirAbs = dirReal
	}
	rel, err := filepath.Rel(rootAbs, dirAbs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func safeBundleFilePath(dir, rel string) (fullPath, cleanRel string, err error) {
	rel = filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(rel), "/"))
	if rel == "" || rel == "." {
		rel = AppEntryFile
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("invalid path")
	}
	full := filepath.Join(dir, clean)
	if !bundleFileIsUnderDir(dir, full) {
		return "", "", fmt.Errorf("invalid path")
	}
	return full, filepath.ToSlash(clean), nil
}

func bundleFileIsUnderDir(dir, file string) bool {
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	fileAbs, err := filepath.Abs(file)
	if err != nil {
		return false
	}
	dirReal, err := filepath.EvalSymlinks(dirAbs)
	if err == nil {
		dirAbs = dirReal
	}
	fileReal, err := filepath.EvalSymlinks(fileAbs)
	if err == nil {
		fileAbs = fileReal
	}
	rel, err := filepath.Rel(dirAbs, fileAbs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func mimeForPath(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".png":
		return "image/png"
	case ".svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}

// ListBundleFiles lists relative paths under bundle (max depth 3).
func ListBundleFiles(dir string, max int) ([]string, error) {
	if max <= 0 {
		max = 200
	}
	var out []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, ".") {
			return nil
		}
		out = append(out, rel)
		if len(out) >= max {
			return fs.SkipAll
		}
		return nil
	})
	return out, nil
}
