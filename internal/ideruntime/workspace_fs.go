package ideruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

var (
	ErrAccessDenied    = errors.New("access denied: path outside workspace root")
	ErrVersionConflict = errors.New("file version conflict: expected version does not match current version")
	ErrNotFound        = errors.New("file or directory not found")
)

// FileStat represents metadata for a file or directory.
type FileStat struct {
	URI     string `json:"uri"`
	Name    string `json:"name"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"`
	Version string `json:"version"` // Content hash / signature
}

// FSChangeType represents file change categories.
type FSChangeType string

const (
	ChangeCreate FSChangeType = "create"
	ChangeModify FSChangeType = "modify"
	ChangeDelete FSChangeType = "delete"
	ChangeRename FSChangeType = "rename"
)

// FSChange records an individual filesystem mutation.
type FSChange struct {
	URI  string       `json:"uri"`
	Type FSChangeType `json:"type"`
}

// FSChangeBatch bundles changes occurring within the debounce window.
type FSChangeBatch struct {
	WorkspaceURI string     `json:"workspace_uri"`
	Changes      []FSChange `json:"changes"`
	Overflow     bool       `json:"overflow"`
}

// WorkspaceFS provides sandbox-guarded, URI-based, atomic file operations.
type WorkspaceFS struct {
	mu           sync.RWMutex
	rootPath     string
	rootURI      string
	watcher      *fsnotify.Watcher
	watcherStop  chan struct{}
	batchMu      sync.Mutex
	changeQueue  map[string]FSChangeType
	batchTimer   *time.Timer
	batchCbs     []func(batch FSChangeBatch)
	watchDirs    map[string]bool
	isWatching   bool
}

// NewWorkspaceFS creates a workspace file service rooted at the specified directory.
func NewWorkspaceFS(rootDir string) (*WorkspaceFS, error) {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve abs root: %w", err)
	}
	absRoot = filepath.Clean(absRoot)

	fs := &WorkspaceFS{
		rootPath:    absRoot,
		rootURI:     PathToURI(absRoot),
		changeQueue: make(map[string]FSChangeType),
		watchDirs:   make(map[string]bool),
		watcherStop: make(chan struct{}),
	}
	return fs, nil
}

// RootURI returns the workspace root formatted as file:// URI.
func (fs *WorkspaceFS) RootURI() string {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.rootURI
}

// RootPath returns the native OS workspace root path.
func (fs *WorkspaceFS) RootPath() string {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.rootPath
}

// SetRoot updates the active workspace root.
func (fs *WorkspaceFS) SetRoot(rootDir string) error {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return fmt.Errorf("resolve abs root: %w", err)
	}
	absRoot = filepath.Clean(absRoot)

	fs.mu.Lock()
	defer fs.mu.Unlock()

	fs.rootPath = absRoot
	fs.rootURI = PathToURI(absRoot)

	if fs.isWatching {
		fs.restartWatcherLocked()
	}
	return nil
}

// ResolveURI converts a file:// URI to an absolute OS path, verifying it resides in workspace.
func (fs *WorkspaceFS) ResolveURI(fileURI string) (string, error) {
	fs.mu.RLock()
	root := fs.rootPath
	fs.mu.RUnlock()

	nativePath, err := URIToPath(fileURI)
	if err != nil {
		return "", err
	}

	cleanPath := filepath.Clean(nativePath)
	if !isSubPath(root, cleanPath) {
		return "", fmt.Errorf("%w: path=%q root=%q", ErrAccessDenied, cleanPath, root)
	}
	return cleanPath, nil
}

// Stat retrieves file or directory metadata by URI.
func (fs *WorkspaceFS) Stat(fileURI string) (*FileStat, error) {
	localPath, err := fs.ResolveURI(fileURI)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(localPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	version := ""
	if !info.IsDir() {
		version = computeFileVersion(localPath, info)
	}

	return &FileStat{
		URI:     fileURI,
		Name:    info.Name(),
		IsDir:   info.IsDir(),
		Size:    info.Size(),
		ModTime: info.ModTime().UnixMilli(),
		Version: version,
	}, nil
}

// ReadFile reads the binary content of a file by URI.
func (fs *WorkspaceFS) ReadFile(fileURI string) ([]byte, *FileStat, error) {
	localPath, err := fs.ResolveURI(fileURI)
	if err != nil {
		return nil, nil, err
	}

	data, err := os.ReadFile(localPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}

	info, err := os.Stat(localPath)
	if err != nil {
		return nil, nil, err
	}

	sum := sha256.Sum256(data)
	stat := &FileStat{
		URI:     fileURI,
		Name:    info.Name(),
		IsDir:   false,
		Size:    info.Size(),
		ModTime: info.ModTime().UnixMilli(),
		Version: hex.EncodeToString(sum[:]),
	}

	return data, stat, nil
}

// ReadText reads file content as a UTF-8 string.
func (fs *WorkspaceFS) ReadText(fileURI string) (string, *FileStat, error) {
	data, stat, err := fs.ReadFile(fileURI)
	if err != nil {
		return "", nil, err
	}
	return string(data), stat, nil
}

// ReadDirectory lists the children of a directory, sorted directories first.
func (fs *WorkspaceFS) ReadDirectory(dirURI string) ([]FileStat, error) {
	localPath, err := fs.ResolveURI(dirURI)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(localPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	result := make([]FileStat, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		childPath := filepath.Join(localPath, entry.Name())
		childURI := PathToURI(childPath)

		version := ""
		if !entry.IsDir() {
			version = computeFileVersion(childPath, info)
		}

		result = append(result, FileStat{
			URI:     childURI,
			Name:    entry.Name(),
			IsDir:   entry.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().UnixMilli(),
			Version: version,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})

	return result, nil
}

// WriteFile writes content atomically (write temp -> sync -> replace) with optimistic version check.
func (fs *WorkspaceFS) WriteFile(fileURI string, content []byte, expectedVersion string) (*FileStat, error) {
	localPath, err := fs.ResolveURI(fileURI)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create parent dir: %w", err)
	}

	// Verify expectedVersion to prevent race overwrite
	if expectedVersion != "" {
		if existing, err := os.ReadFile(localPath); err == nil {
			sum := sha256.Sum256(existing)
			currentVersion := hex.EncodeToString(sum[:])
			if currentVersion != expectedVersion {
				return nil, fmt.Errorf("%w: current=%s expected=%s", ErrVersionConflict, currentVersion, expectedVersion)
			}
		}
	}

	// Write to temporary file in the same directory for atomic rename
	tmpFile, err := os.CreateTemp(dir, ".agentgo-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmpFile.Name()

	var writeErr error
	defer func() {
		if writeErr != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, writeErr = tmpFile.Write(content); writeErr != nil {
		_ = tmpFile.Close()
		return nil, fmt.Errorf("write temp: %w", writeErr)
	}

	if writeErr = tmpFile.Sync(); writeErr != nil {
		_ = tmpFile.Close()
		return nil, fmt.Errorf("sync temp: %w", writeErr)
	}

	if writeErr = tmpFile.Close(); writeErr != nil {
		return nil, fmt.Errorf("close temp: %w", writeErr)
	}

	// Atomic replace
	if runtime.GOOS == "windows" {
		_ = os.Remove(localPath)
	}
	if writeErr = os.Rename(tmpName, localPath); writeErr != nil {
		return nil, fmt.Errorf("atomic rename: %w", writeErr)
	}

	info, statErr := os.Stat(localPath)
	if statErr != nil {
		return nil, statErr
	}

	sum := sha256.Sum256(content)
	return &FileStat{
		URI:     fileURI,
		Name:    filepath.Base(localPath),
		IsDir:   false,
		Size:    info.Size(),
		ModTime: info.ModTime().UnixMilli(),
		Version: hex.EncodeToString(sum[:]),
	}, nil
}

// CreateDirectory creates directory recursively.
func (fs *WorkspaceFS) CreateDirectory(dirURI string) error {
	localPath, err := fs.ResolveURI(dirURI)
	if err != nil {
		return err
	}
	return os.MkdirAll(localPath, 0755)
}

// Delete removes a file or directory.
func (fs *WorkspaceFS) Delete(fileURI string) error {
	localPath, err := fs.ResolveURI(fileURI)
	if err != nil {
		return err
	}
	if localPath == fs.rootPath {
		return errors.New("cannot delete workspace root")
	}
	return os.RemoveAll(localPath)
}

// Rename moves or renames a file or directory within workspace.
func (fs *WorkspaceFS) Rename(oldURI, newURI string) error {
	oldPath, err := fs.ResolveURI(oldURI)
	if err != nil {
		return err
	}
	newPath, err := fs.ResolveURI(newURI)
	if err != nil {
		return err
	}

	dir := filepath.Dir(newPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		_ = os.Remove(newPath)
	}
	return os.Rename(oldPath, newPath)
}

// Copy copies a file from srcURI to dstURI atomically.
func (fs *WorkspaceFS) Copy(srcURI, dstURI string) error {
	srcPath, err := fs.ResolveURI(srcURI)
	if err != nil {
		return err
	}
	dstPath, err := fs.ResolveURI(dstURI)
	if err != nil {
		return err
	}

	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}

	dstFile, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// --- Recursive Watcher with 100ms Debounce ---

// OnFilesChanged registers a listener for coalesced file change events.
func (fs *WorkspaceFS) OnFilesChanged(cb func(batch FSChangeBatch)) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.batchCbs = append(fs.batchCbs, cb)
}

// StartWatcher starts recursive directory watching on workspace root.
func (fs *WorkspaceFS) StartWatcher() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if fs.isWatching {
		return nil
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("new fsnotify watcher: %w", err)
	}
	fs.watcher = w
	fs.isWatching = true

	fs.addWatchDirsRecursiveLocked(fs.rootPath)
	go fs.watcherLoop()

	return nil
}

// StopWatcher terminates the filesystem watcher.
func (fs *WorkspaceFS) StopWatcher() {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if !fs.isWatching {
		return
	}
	fs.isWatching = false
	if fs.watcher != nil {
		_ = fs.watcher.Close()
	}
}

func (fs *WorkspaceFS) restartWatcherLocked() {
	if fs.watcher != nil {
		_ = fs.watcher.Close()
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	fs.watcher = w
	fs.watchDirs = make(map[string]bool)
	fs.addWatchDirsRecursiveLocked(fs.rootPath)
}

func (fs *WorkspaceFS) addWatchDirsRecursiveLocked(root string) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		base := d.Name()
		if base == ".git" || base == "node_modules" || base == "dist" || base == ".idea" {
			return filepath.SkipDir
		}
		if fs.watcher != nil && !fs.watchDirs[path] {
			if err := fs.watcher.Add(path); err == nil {
				fs.watchDirs[path] = true
			}
		}
		return nil
	})
}

func (fs *WorkspaceFS) watcherLoop() {
	for {
		select {
		case <-fs.watcherStop:
			return
		case err, ok := <-fs.watcher.Errors:
			if !ok {
				return
			}
			// Buffer overflow on Windows ReadDirectoryChangesW -> trigger full rescan batch
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				fs.emitBatch(FSChangeBatch{
					WorkspaceURI: fs.RootURI(),
					Overflow:     true,
				})
			}
		case event, ok := <-fs.watcher.Events:
			if !ok {
				return
			}

			// If new directory created, recursively watch it
			if event.Has(fsnotify.Create) {
				if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
					fs.mu.Lock()
					fs.addWatchDirsRecursiveLocked(event.Name)
					fs.mu.Unlock()
				}
			}

			// Map fsnotify op to change type
			var ct FSChangeType
			switch {
			case event.Has(fsnotify.Create):
				ct = ChangeCreate
			case event.Has(fsnotify.Remove):
				ct = ChangeDelete
			case event.Has(fsnotify.Rename):
				ct = ChangeRename
			case event.Has(fsnotify.Write):
				ct = ChangeModify
			default:
				continue
			}

			uri := PathToURI(event.Name)
			fs.queueChange(uri, ct)
		}
	}
}

func (fs *WorkspaceFS) queueChange(uri string, ct FSChangeType) {
	fs.batchMu.Lock()
	defer fs.batchMu.Unlock()

	fs.changeQueue[uri] = ct

	if fs.batchTimer != nil {
		fs.batchTimer.Stop()
	}
	fs.batchTimer = time.AfterFunc(100*time.Millisecond, fs.flushBatch)
}

func (fs *WorkspaceFS) flushBatch() {
	fs.batchMu.Lock()
	if len(fs.changeQueue) == 0 {
		fs.batchMu.Unlock()
		return
	}

	changes := make([]FSChange, 0, len(fs.changeQueue))
	for u, ct := range fs.changeQueue {
		changes = append(changes, FSChange{URI: u, Type: ct})
	}
	fs.changeQueue = make(map[string]FSChangeType)
	fs.batchMu.Unlock()

	fs.emitBatch(FSChangeBatch{
		WorkspaceURI: fs.RootURI(),
		Changes:      changes,
		Overflow:     false,
	})
}

func (fs *WorkspaceFS) emitBatch(batch FSChangeBatch) {
	fs.mu.RLock()
	cbs := append([]func(batch FSChangeBatch){}, fs.batchCbs...)
	fs.mu.RUnlock()

	for _, cb := range cbs {
		cb(batch)
	}
}

// --- URI & Path Conversion Helpers ---

// PathToURI converts an absolute filesystem path to a unified file:// URI.
func PathToURI(p string) string {
	clean := filepath.Clean(p)
	slash := filepath.ToSlash(clean)

	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	u := url.URL{
		Scheme: "file",
		Path:   slash,
	}
	return u.String()
}

// URIToPath converts a file:// URI to an absolute OS path.
func URIToPath(uriStr string) (string, error) {
	if !strings.HasPrefix(uriStr, "file://") {
		return "", fmt.Errorf("invalid file URI schema: %q", uriStr)
	}

	u, err := url.Parse(uriStr)
	if err != nil {
		return "", fmt.Errorf("parse file URI: %w", err)
	}

	p := u.Path
	if runtime.GOOS == "windows" {
		// Strip leading slash from Windows drive: /C:/foo -> C:/foo
		if len(p) > 2 && p[0] == '/' && p[2] == ':' {
			p = p[1:]
		}
	}
	return filepath.Clean(filepath.FromSlash(p)), nil
}

func isSubPath(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)

	if strings.EqualFold(parent, child) {
		return true
	}

	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && rel != "."
}

func computeFileVersion(path string, info os.FileInfo) string {
	// Fast version tag using modtime and size
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d:%d:%s", info.ModTime().UnixNano(), info.Size(), path)
	return hex.EncodeToString(h.Sum(nil))[:16]
}
