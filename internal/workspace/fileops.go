package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Entry is one row of a directory listing (JSON shape is consumed by the UI).
type Entry struct {
	Name      string `json:"name"`
	IsDir     bool   `json:"is_dir"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	ModTime   string `json:"mod_time"`
	GitStatus string `json:"git_status,omitempty"`
}

// List returns the visible entries of root/rel (dirs first, then by name).
// Unreadable or escaping paths yield nil.
func List(root, rel string) []Entry {
	clean, target, err := FullPath(root, rel, true)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil
	}
	statuses := GitStatuses(root)
	var out []Entry
	for _, e := range entries {
		if ShouldSkipEntry(root, clean, e.Name(), e.IsDir()) {
			continue
		}
		var size int64
		modTime := ""
		if info, _ := e.Info(); info != nil {
			size = info.Size()
			modTime = info.ModTime().Format(time.RFC3339)
		}
		p := filepath.ToSlash(filepath.Join(clean, e.Name()))
		out = append(out, Entry{
			Name: e.Name(), IsDir: e.IsDir(), Path: p, Size: size, ModTime: modTime,
			GitStatus: GitStatusForEntry(statuses, p, e.IsDir()),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// TextFile is a workspace file loaded for the editor.
type TextFile struct {
	Path    string
	Size    int64
	Content string
}

// ErrTooLarge is returned (wrapped) by ReadText when the file exceeds MaxPreviewBytes.
var ErrTooLarge = errors.New("file is too large for preview")

// ReadText reads a text file below root. On ErrTooLarge the returned TextFile
// still carries Path and Size so the UI can show them.
func ReadText(root, rel string) (TextFile, error) {
	clean, full, err := FullPath(root, rel, false)
	if err != nil {
		return TextFile{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return TextFile{}, err
	}
	if info.IsDir() {
		return TextFile{}, errors.New("path is a directory")
	}
	if info.Size() > MaxPreviewBytes {
		return TextFile{Path: clean, Size: info.Size()},
			fmt.Errorf("%w (%d KB limit)", ErrTooLarge, MaxPreviewBytes/1024)
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return TextFile{}, err
	}
	return TextFile{Path: clean, Size: info.Size(), Content: string(b)}, nil
}

// WriteText writes content, creating parent directories.
func WriteText(root, rel, content string) (clean string, err error) {
	clean, full, err := FullPath(root, rel, false)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", fmt.Errorf("failed to create parent directories: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("failed to write file: %v", err)
	}
	return clean, nil
}

// CreateFile creates an empty file; it fails if the file already exists.
func CreateFile(root, rel string) (string, error) {
	clean, full, err := FullPath(root, rel, false)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(full, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	_ = f.Close()
	return clean, nil
}

// CreateDir creates a directory (and parents).
func CreateDir(root, rel string) (string, error) {
	clean, full, err := FullPath(root, rel, false)
	if err != nil {
		return "", err
	}
	return clean, os.MkdirAll(full, 0o755)
}

// Delete removes a file or directory tree.
func Delete(root, rel string) (string, error) {
	clean, full, err := FullPath(root, rel, false)
	if err != nil {
		return "", err
	}
	return clean, os.RemoveAll(full)
}

// Rename moves oldRel to newRel (both inside root) and returns the new clean path.
func Rename(root, oldRel, newRel string) (string, error) {
	_, oldFull, err := FullPath(root, oldRel, false)
	if err != nil {
		return "", err
	}
	newClean, newFull, err := FullPath(root, newRel, false)
	if err != nil {
		return "", err
	}
	return newClean, os.Rename(oldFull, newFull)
}

// ShellResult is the outcome of RunShell.
type ShellResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

// RunShell runs a command line in root with the platform shell (30s timeout).
func RunShell(root, command string) ShellResult {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Dir = root
	HideExecWindow(cmd)

	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			code = 1
		}
	}
	return ShellResult{Stdout: out.String(), Stderr: errb.String(), ExitCode: code, Err: err}
}
