package bridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentgo/internal/ideruntime"
)

// --- File System Operations ---

// FSStat retrieves metadata for a file or directory by URI.
// If the file does not exist or access is denied, returns (nil, nil) to prevent Wails 422 HTTP error.
func (s *AppService) FSStat(uri string) (*ideruntime.FileStat, error) {
	if uri == "" {
		return nil, nil
	}
	fs := s.rt.WorkspaceFS()
	if fs != nil {
		stat, err := fs.Stat(uri)
		if err == nil && stat != nil {
			return stat, nil
		}
	}

	// Fallback to real OS path for system directories (e.g. .opensumi, AppData, user home)
	localPath, pathErr := ideruntime.URIToPath(uri)
	if pathErr == nil && localPath != "" {
		info, statErr := os.Stat(localPath)
		if statErr == nil {
			return &ideruntime.FileStat{
				URI:     uri,
				Name:    info.Name(),
				IsDir:   info.IsDir(),
				Size:    info.Size(),
				ModTime: info.ModTime().UnixMilli(),
			}, nil
		}
	}

	// File does not exist - return nil, nil so Wails responds with 200 null instead of 422
	return nil, nil
}

// FSReadText reads file content as a string.
// If file does not exist, returns safe empty response instead of error.
func (s *AppService) FSReadText(uri string) (map[string]any, error) {
	if uri == "" {
		return map[string]any{"uri": "", "content": "", "exists": false}, nil
	}
	fs := s.rt.WorkspaceFS()
	if fs != nil {
		text, stat, err := fs.ReadText(uri)
		if err == nil {
			return map[string]any{
				"uri":     uri,
				"content": text,
				"stat":    stat,
				"exists":  true,
			}, nil
		}
	}

	// Fallback to real OS path
	localPath, pathErr := ideruntime.URIToPath(uri)
	if pathErr == nil && localPath != "" {
		data, readErr := os.ReadFile(localPath)
		if readErr == nil {
			info, _ := os.Stat(localPath)
			var modTime int64
			if info != nil {
				modTime = info.ModTime().UnixMilli()
			}
			return map[string]any{
				"uri":     uri,
				"content": string(data),
				"stat": &ideruntime.FileStat{
					URI:     uri,
					Name:    filepath.Base(localPath),
					IsDir:   false,
					Size:    int64(len(data)),
					ModTime: modTime,
				},
				"exists": true,
			}, nil
		}
	}

	return map[string]any{
		"uri":     uri,
		"content": "",
		"exists":  false,
	}, nil
}

// FSReadDirectory lists children of a directory.
func (s *AppService) FSReadDirectory(dirURI string) ([]ideruntime.FileStat, error) {
	fs := s.rt.WorkspaceFS()
	if fs != nil {
		if dirURI == "" {
			dirURI = fs.RootURI()
		}
		entries, err := fs.ReadDirectory(dirURI)
		if err == nil {
			return entries, nil
		}
	}

	// Fallback to real OS path
	localPath, pathErr := ideruntime.URIToPath(dirURI)
	if pathErr == nil && localPath != "" {
		entries, readErr := os.ReadDir(localPath)
		if readErr == nil {
			res := make([]ideruntime.FileStat, 0, len(entries))
			for _, entry := range entries {
				info, err := entry.Info()
				var size int64
				var modTime int64
				if err == nil {
					size = info.Size()
					modTime = info.ModTime().UnixMilli()
				}
				childPath := filepath.Join(localPath, entry.Name())
				res = append(res, ideruntime.FileStat{
					URI:     ideruntime.PathToURI(childPath),
					Name:    entry.Name(),
					IsDir:   entry.IsDir(),
					Size:    size,
					ModTime: modTime,
				})
			}
			return res, nil
		}
	}

	return []ideruntime.FileStat{}, nil
}

// FSWriteFile writes file content atomically with optional optimistic lock version.
func (s *AppService) FSWriteFile(uri string, content string, expectedVersion string) (*ideruntime.FileStat, error) {
	fs := s.rt.WorkspaceFS()
	if fs != nil {
		stat, err := fs.WriteFile(uri, []byte(content), expectedVersion)
		if err == nil {
			return stat, nil
		}
	}

	// Fallback to real OS path (for global storage/config outside workspace)
	localPath, pathErr := ideruntime.URIToPath(uri)
	if pathErr == nil && localPath != "" {
		dir := filepath.Dir(localPath)
		if dir != "" {
			_ = os.MkdirAll(dir, 0755)
		}
		writeErr := os.WriteFile(localPath, []byte(content), 0644)
		if writeErr == nil {
			info, _ := os.Stat(localPath)
			var modTime int64
			if info != nil {
				modTime = info.ModTime().UnixMilli()
			}
			return &ideruntime.FileStat{
				URI:     uri,
				Name:    filepath.Base(localPath),
				IsDir:   false,
				Size:    int64(len(content)),
				ModTime: modTime,
			}, nil
		}
		return nil, writeErr
	}

	return nil, errors.New("cannot write file")
}

// FSCreateDirectory creates a directory recursively.
func (s *AppService) FSCreateDirectory(dirURI string) error {
	fs := s.rt.WorkspaceFS()
	if fs != nil {
		err := fs.CreateDirectory(dirURI)
		if err == nil {
			return nil
		}
	}

	// Fallback to real OS path
	localPath, pathErr := ideruntime.URIToPath(dirURI)
	if pathErr == nil && localPath != "" {
		return os.MkdirAll(localPath, 0755)
	}
	return nil
}

// FSDelete removes a file or directory.
func (s *AppService) FSDelete(uri string) error {
	fs := s.rt.WorkspaceFS()
	if fs != nil {
		_ = fs.Delete(uri)
	}
	localPath, pathErr := ideruntime.URIToPath(uri)
	if pathErr == nil && localPath != "" {
		_ = os.RemoveAll(localPath)
	}
	return nil
}

// FSRename moves or renames a file or directory.
func (s *AppService) FSRename(oldURI, newURI string) error {
	fs := s.rt.WorkspaceFS()
	if fs != nil {
		err := fs.Rename(oldURI, newURI)
		if err == nil {
			return nil
		}
	}
	oldPath, err1 := ideruntime.URIToPath(oldURI)
	newPath, err2 := ideruntime.URIToPath(newURI)
	if err1 == nil && err2 == nil {
		return os.Rename(oldPath, newPath)
	}
	return nil
}

// FSCopy copies a file.
func (s *AppService) FSCopy(srcURI, dstURI string) error {
	fs := s.rt.WorkspaceFS()
	if fs != nil {
		err := fs.Copy(srcURI, dstURI)
		if err == nil {
			return nil
		}
	}
	srcPath, err1 := ideruntime.URIToPath(srcURI)
	dstPath, err2 := ideruntime.URIToPath(dstURI)
	if err1 == nil && err2 == nil {
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, 0644)
	}
	return nil
}

// FSRoot returns the workspace root URI and local path.
func (s *AppService) FSRoot() map[string]string {
	fs := s.rt.WorkspaceFS()
	if fs == nil {
		return map[string]string{
			"uri":  ideruntime.PathToURI(s.rt.WorkspaceRoot()),
			"path": s.rt.WorkspaceRoot(),
		}
	}
	return map[string]string{
		"uri":  fs.RootURI(),
		"path": fs.RootPath(),
	}
}

// --- Terminal Operations ---

// TerminalCreate spawns a new interactive PTY terminal.
func (s *AppService) TerminalCreate(id, shell, cwd string, cols, rows int) (ideruntime.TerminalInfo, error) {
	ts := s.rt.TerminalService()
	if ts == nil {
		return ideruntime.TerminalInfo{}, errors.New("terminal service not initialized")
	}
	if cwd == "" {
		cwd = s.rt.WorkspaceRoot()
	} else if strings.HasPrefix(cwd, "file:") {
		if p, err := ideruntime.URIToPath(cwd); err == nil && p != "" {
			cwd = p
		}
	}
	return ts.CreateTerminal(id, shell, cwd, cols, rows)
}

// TerminalPoll fetches pending output from the terminal's ring buffer.
func (s *AppService) TerminalPoll(id string) string {
	ts := s.rt.TerminalService()
	if ts == nil {
		return ""
	}
	return ts.PollTerminal(id)
}

// TerminalWrite sends user keystrokes / commands to the PTY.
func (s *AppService) TerminalWrite(id, data string) error {
	ts := s.rt.TerminalService()
	if ts == nil {
		return errors.New("terminal service not initialized")
	}
	return ts.WriteTerminal(id, data)
}

// TerminalResize resizes terminal dimensions.
func (s *AppService) TerminalResize(id string, cols, rows int) error {
	ts := s.rt.TerminalService()
	if ts == nil {
		return errors.New("terminal service not initialized")
	}
	return ts.ResizeTerminal(id, cols, rows)
}

// TerminalClose terminates the terminal process.
func (s *AppService) TerminalClose(id string) error {
	ts := s.rt.TerminalService()
	if ts == nil {
		return nil
	}
	return ts.CloseTerminal(id)
}

// TerminalList lists active terminals.
func (s *AppService) TerminalList() []ideruntime.TerminalInfo {
	ts := s.rt.TerminalService()
	if ts == nil {
		return []ideruntime.TerminalInfo{}
	}
	return ts.ListTerminals()
}

// --- Search Operations ---

// SearchFiles searches for matching file paths across the workspace.
func (s *AppService) SearchFiles(pattern string, maxResults int) ([]string, error) {
	ss := s.rt.SearchService()
	if ss == nil {
		return nil, errors.New("search service not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return ss.SearchFiles(ctx, pattern, maxResults)
}

// SearchText searches for text content using ripgrep JSON mode.
func (s *AppService) SearchText(query ideruntime.SearchQuery) ([]ideruntime.SearchResult, error) {
	ss := s.rt.SearchService()
	if ss == nil {
		return nil, errors.New("search service not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return ss.SearchText(ctx, query)
}

// --- Git Operations ---

// GitStatus returns the current Git status.
func (s *AppService) GitStatus() (ideruntime.GitStatusResult, error) {
	gs := s.rt.GitService()
	if gs == nil {
		return ideruntime.GitStatusResult{}, errors.New("git service not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return gs.GetStatus(ctx)
}

// GitDiff retrieves the diff for a specific file.
func (s *AppService) GitDiff(fileURI string, staged bool) (string, error) {
	gs := s.rt.GitService()
	if gs == nil {
		return "", errors.New("git service not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return gs.GetDiff(ctx, fileURI, staged)
}

// GitStage adds files to the Git index.
func (s *AppService) GitStage(fileURIs []string) error {
	gs := s.rt.GitService()
	if gs == nil {
		return errors.New("git service not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return gs.Stage(ctx, fileURIs...)
}

// GitUnstage removes files from the Git index.
func (s *AppService) GitUnstage(fileURIs []string) error {
	gs := s.rt.GitService()
	if gs == nil {
		return errors.New("git service not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return gs.Unstage(ctx, fileURIs...)
}

// GitCommit creates a Git commit.
func (s *AppService) GitCommit(message string) error {
	gs := s.rt.GitService()
	if gs == nil {
		return errors.New("git service not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return gs.Commit(ctx, message)
}

// Unused import helper
var _ = fmt.Sprintf
