package bridge

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type WorkspaceInfo struct {
	Root              string `json:"root"`
	Name              string `json:"name"`
	GitRoot           string `json:"git_root,omitempty"`
	Branch            string `json:"branch,omitempty"`
	DirtyCount        int    `json:"dirty_count"`
	IsGit             bool   `json:"is_git"`
	Writable          bool   `json:"writable"`
	Warning           string `json:"warning,omitempty"`
	PreviewLimitBytes int64  `json:"preview_limit_bytes"`
	WatchMode         string `json:"watch_mode"`
}

func normalizeWorkspaceRoot(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("workspace root is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if eval, err := filepath.EvalSymlinks(abs); err == nil {
		abs = eval
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace root is not a directory")
	}
	if isFilesystemRoot(abs) {
		return "", fmt.Errorf("workspace root is too broad: %s", abs)
	}
	return filepath.Clean(abs), nil
}

func isFilesystemRoot(abs string) bool {
	abs = filepath.Clean(abs)
	volume := filepath.VolumeName(abs)
	root := string(filepath.Separator)
	if volume != "" {
		root = volume + string(filepath.Separator)
	}
	return strings.EqualFold(abs, filepath.Clean(root))
}

func workspaceRootWarning(abs string) string {
	home, _ := os.UserHomeDir()
	if home != "" && samePath(abs, home) {
		return "工作区指向用户 Home，建议选择更小的项目目录。"
	}
	base := strings.ToLower(filepath.Base(abs))
	if base == "downloads" || base == "desktop" || base == "documents" {
		return "工作区范围偏大，建议选择具体项目目录，避免 Agent 拉入无关文件。"
	}
	if runtime.GOOS == "windows" && len(filepath.VolumeName(abs)) > 0 && strings.Count(strings.TrimPrefix(abs, filepath.VolumeName(abs)), string(filepath.Separator)) <= 2 {
		return "工作区接近磁盘顶层，建议选择具体仓库目录。"
	}
	return ""
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	if ea, err := filepath.EvalSymlinks(aa); err == nil {
		aa = ea
	}
	if eb, err := filepath.EvalSymlinks(bb); err == nil {
		bb = eb
	}
	return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
}

func buildWorkspaceInfo(root string) WorkspaceInfo {
	root = filepath.Clean(root)
	info := WorkspaceInfo{
		Root:              root,
		Name:              filepath.Base(root),
		PreviewLimitBytes: maxWorkspacePreviewBytes,
		WatchMode:         "poll",
		Warning:           workspaceRootWarning(root),
	}
	if info.Name == "." || info.Name == string(filepath.Separator) {
		info.Name = root
	}
	info.Writable = workspaceWritable(root)

	if gitRoot, err := runGit(root, "rev-parse", "--show-toplevel"); err == nil {
		info.GitRoot = strings.TrimSpace(gitRoot)
		info.IsGit = info.GitRoot != ""
	}
	if branch, err := runGit(root, "branch", "--show-current"); err == nil {
		info.Branch = strings.TrimSpace(branch)
	}
	if status, err := runGit(root, "status", "--porcelain"); err == nil {
		for _, line := range strings.Split(status, "\n") {
			if strings.TrimSpace(line) != "" {
				info.DirtyCount++
			}
		}
	}
	return info
}

func workspaceWritable(root string) bool {
	f, err := os.CreateTemp(root, ".agentgo-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}
