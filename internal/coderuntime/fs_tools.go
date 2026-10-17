package coderuntime

// Native file tools: view / write / edit / multiedit.
//
// They replace the Crush-backed implementations behind the Toolbox interface.
// What they keep from Crush is the model-visible contract: parameter names,
// the line-numbered <file> output, the read-before-modify rule, CRLF
// preservation and the exact error texts. What they drop is everything that
// belonged to Crush's app shell: its SQLite file tracker and history, its
// permission service and its session plumbing. Approval is a single hook
// (FSOptions.Approve) and AgentGo's governance layer is the real gate.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// DefaultReadLimit is how many lines view returns when limit is not given.
	DefaultReadLimit = 200
	// MaxViewSize caps the bytes one view call may return.
	MaxViewSize = 200 * 1024
	// MaxLineLength truncates very long lines in view output.
	MaxLineLength = 2000
)

// FSRequest is what Approve is asked about.
type FSRequest struct {
	SessionID   string
	CallID      string
	Tool        string // view | write | edit | multiedit
	Action      string // read | write
	Path        string // absolute
	Description string
}

// FSOptions configures NewFSTools.
type FSOptions struct {
	WorkDir string
	// Approve gates reads outside WorkDir and every write. nil allows all.
	Approve func(ctx context.Context, req FSRequest) bool
	// AfterChange runs after a successful write/edit and returns text (for
	// example language-server diagnostics) appended to the tool result.
	AfterChange func(ctx context.Context, sessionID, callID, path string) string
	// OnRead runs after a successful view, e.g. to warm up a language server.
	OnRead func(ctx context.Context, path string)
}

// fsTools holds the shared state of the four tools.
type fsTools struct {
	opts FSOptions
	root string

	mu    sync.Mutex
	reads map[string]time.Time // sessionID + "\x00" + path -> last read
}

// NewFSTools returns the view, write, edit and multiedit tools.
func NewFSTools(opts FSOptions) []Tool {
	t := &fsTools{opts: opts, reads: map[string]time.Time{}}
	t.root, _ = filepath.Abs(opts.WorkDir)
	return []Tool{t.viewTool(), t.writeTool(), t.editTool(), t.multiEditTool()}
}

func okResult(s string) ToolResult  { return ToolResult{Content: s} }
func errResult(s string) ToolResult { return ToolResult{Content: s, IsError: true} }

const deniedText = "User denied permission"

func (t *fsTools) approve(ctx context.Context, r FSRequest) bool {
	return t.opts.Approve == nil || t.opts.Approve(ctx, r)
}

// resolve joins a model-supplied path onto the workspace. A leading slash is
// absolute on every OS, because models write POSIX-style paths on Windows too.
func (t *fsTools) resolve(p string) string {
	abs := filepath.IsAbs(p)
	if runtime.GOOS == "windows" && !abs {
		abs = strings.HasPrefix(filepath.ToSlash(p), "/")
	}
	if abs {
		return p
	}
	return filepath.Join(t.opts.WorkDir, p)
}

func (t *fsTools) outsideWorkDir(abs string) bool {
	a, err := filepath.Abs(abs)
	if err != nil {
		return true
	}
	rel, err := filepath.Rel(t.root, a)
	return err != nil || strings.HasPrefix(rel, "..")
}

// --- read tracker -----------------------------------------------------------

func trackKey(sessionID, path string) string { return sessionID + "\x00" + filepath.Clean(path) }

func (t *fsTools) recordRead(sessionID, path string) {
	t.mu.Lock()
	t.reads[trackKey(sessionID, path)] = time.Now()
	t.mu.Unlock()
}

func (t *fsTools) lastRead(sessionID, path string) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reads[trackKey(sessionID, path)]
}

// staleCheck enforces "read before you modify": it reports a ready-to-return
// error text when the file was never read or changed after the last read.
func (t *fsTools) staleCheck(sessionID, path string, info os.FileInfo, style staleStyle) string {
	lastRead := t.lastRead(sessionID, path)
	if lastRead.IsZero() && style == styleEdit {
		return "you must read the file before editing it. Use the View tool first"
	}
	// Whole seconds: a read and a write inside one second are not "modified since".
	lastRead = lastRead.Truncate(time.Second)
	mod := info.ModTime().Truncate(time.Second)
	if !mod.After(lastRead) {
		return ""
	}
	if style == styleWrite {
		return fmt.Sprintf("File %s has been modified since it was last read.\nLast modification: %s\nLast read: %s\n\nPlease read the file again before modifying it.",
			path, mod.Format(time.RFC3339), lastRead.Format(time.RFC3339))
	}
	return fmt.Sprintf("file %s has been modified since it was last read (mod time: %s, last read: %s)",
		path, mod.Format(time.RFC3339), lastRead.Format(time.RFC3339))
}

type staleStyle int

const (
	styleEdit staleStyle = iota
	styleWrite
)

// --- line endings ------------------------------------------------------------

func toUnixLineEndings(s string) (string, bool) {
	if strings.Contains(s, "\r\n") {
		return strings.ReplaceAll(s, "\r\n", "\n"), true
	}
	return s, false
}

func toWindowsLineEndings(s string) string {
	if strings.Contains(s, "\r\n") {
		return s
	}
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// --- view ----------------------------------------------------------------------

type viewArgs struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

func (t *fsTools) viewTool() Tool {
	return Tool{
		Name: "view",
		Description: fmt.Sprintf("Read a text file. Output is line-numbered inside <file> tags. Reads %d lines from `offset` (0-based) unless `limit` says otherwise; "+
			"when more lines remain the output tells you which offset to continue from. Lines longer than %d characters are truncated and a single call returns at most %d KB. "+
			"Always view a file before editing or overwriting it.", DefaultReadLimit, MaxLineLength, MaxViewSize/1024),
		Schema: objectSchema(map[string]any{
			"file_path": strProp("The path to the file to read"),
			"offset":    intProp("The line number to start reading from (0-based)"),
			"limit":     intProp(fmt.Sprintf("The number of lines to read (defaults to %d)", DefaultReadLimit)),
		}, "file_path"),
		Parallel: true,
		Run: func(ctx context.Context, sessionID, callID, argsJSON string) (ToolResult, error) {
			var a viewArgs
			if r, bad := parseArgs(argsJSON, &a); bad {
				return r, nil
			}
			return t.view(ctx, sessionID, callID, a)
		},
	}
}

func (t *fsTools) view(ctx context.Context, sessionID, callID string, a viewArgs) (ToolResult, error) {
	if a.FilePath == "" {
		return errResult("file_path is required"), nil
	}
	path := t.resolve(a.FilePath)
	abs, err := filepath.Abs(path)
	if err != nil {
		return ToolResult{}, fmt.Errorf("error resolving file path: %w", err)
	}
	if t.outsideWorkDir(abs) {
		if !t.approve(ctx, FSRequest{SessionID: sessionID, CallID: callID, Tool: "view", Action: "read", Path: abs,
			Description: "Read file outside working directory: " + abs}) {
			return errResult(deniedText), nil
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return errResult(notFoundText(path)), nil
		}
		return ToolResult{}, fmt.Errorf("error accessing file: %w", err)
	}
	if info.IsDir() {
		return errResult("Path is a directory, not a file: " + path), nil
	}
	if a.Limit <= 0 {
		a.Limit = DefaultReadLimit
	}
	if isImageFile(path) {
		return errResult("This is an image file; the code view tool only reads text: " + path), nil
	}

	content, hasMore, err := readTextFile(path, a.Offset, a.Limit, MaxViewSize)
	if err != nil {
		var big tooLargeError
		if errors.As(err, &big) {
			return errResult(fmt.Sprintf("Content section is too large (%d bytes). Maximum size is %d bytes", big.size, big.max)), nil
		}
		return ToolResult{}, fmt.Errorf("error reading file: %w", err)
	}
	if !utf8.ValidString(content) {
		return errResult("File content is not valid UTF-8"), nil
	}

	var out strings.Builder
	out.WriteString("<file>\n")
	out.WriteString(addLineNumbers(content, a.Offset+1))
	if hasMore {
		fmt.Fprintf(&out, "\n\n(File has more lines. Use 'offset' parameter to read beyond line %d)",
			a.Offset+len(strings.Split(content, "\n")))
	}
	out.WriteString("\n</file>\n")

	t.recordRead(sessionID, path)
	if t.opts.OnRead != nil {
		t.opts.OnRead(ctx, path)
	}
	return okResult(out.String()), nil
}

// notFoundText lists up to three similarly named siblings.
func notFoundText(path string) string {
	dir, base := filepath.Dir(path), strings.ToLower(filepath.Base(path))
	if entries, err := os.ReadDir(dir); err == nil {
		var near []string
		for _, e := range entries {
			n := strings.ToLower(e.Name())
			if strings.Contains(n, base) || strings.Contains(base, n) {
				near = append(near, filepath.Join(dir, e.Name()))
				if len(near) >= 3 {
					break
				}
			}
		}
		if len(near) > 0 {
			return fmt.Sprintf("File not found: %s\n\nDid you mean one of these?\n%s", path, strings.Join(near, "\n"))
		}
	}
	return "File not found: " + path
}

func isImageFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	}
	return false
}

type tooLargeError struct{ size, max int }

func (e tooLargeError) Error() string {
	return fmt.Sprintf("content section is too large (%d bytes). Maximum size is %d bytes", e.size, e.max)
}

// addLineNumbers renders "%6d|line", starting at startLine.
func addLineNumbers(content string, startLine int) string {
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = fmt.Sprintf("%6d|%s", i+startLine, strings.TrimSuffix(line, "\r"))
	}
	return strings.Join(out, "\n")
}

// readTextFile reads limit lines after skipping offset lines and reports
// whether more lines follow.
func readTextFile(path string, offset, limit, maxSize int) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	r := bufio.NewReader(f)
	for skipped := 0; skipped < offset; skipped++ {
		if _, err := r.ReadString('\n'); err != nil {
			if err == io.EOF {
				return "", false, nil
			}
			return "", false, err
		}
	}

	lines := make([]string, 0, min(limit, DefaultReadLimit))
	size := 0
	for len(lines) < limit {
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", false, err
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if len(line) > MaxLineLength {
			line = strings.ToValidUTF8(line[:MaxLineLength], "") + "..."
		}
		projected := size + len(line)
		if len(lines) > 0 {
			projected++
		}
		if maxSize > 0 && projected > maxSize {
			return "", false, tooLargeError{size: projected, max: maxSize}
		}
		size = projected
		lines = append(lines, line)
		if err == io.EOF {
			break
		}
	}

	hasMore := false
	if len(lines) == limit {
		rest, perr := r.ReadString('\n')
		hasMore = len(rest) > 0 || perr == nil
	}
	return strings.Join(lines, "\n"), hasMore, nil
}

// --- write ---------------------------------------------------------------------

type writeArgs struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func (t *fsTools) writeTool() Tool {
	return Tool{
		Name: "write",
		Description: "Create a file or overwrite it with `content` (parent directories are created). " +
			"An existing file must have been viewed first and must not have changed since. " +
			"Prefer edit/multiedit for changing part of a file; use write for new files and full rewrites.",
		Schema: objectSchema(map[string]any{
			"file_path": strProp("The path to the file to write"),
			"content":   strProp("The content to write to the file"),
		}, "file_path", "content"),
		Run: func(ctx context.Context, sessionID, callID, argsJSON string) (ToolResult, error) {
			var a writeArgs
			if r, bad := parseArgs(argsJSON, &a); bad {
				return r, nil
			}
			return t.write(ctx, sessionID, callID, a)
		},
	}
}

func (t *fsTools) write(ctx context.Context, sessionID, callID string, a writeArgs) (ToolResult, error) {
	if a.FilePath == "" {
		return errResult("file_path is required"), nil
	}
	path := t.resolve(a.FilePath)

	oldContent := ""
	info, err := os.Stat(path)
	switch {
	case err == nil:
		if info.IsDir() {
			return errResult("Path is a directory, not a file: " + path), nil
		}
		if msg := t.staleCheck(sessionID, path, info, styleWrite); msg != "" {
			return errResult(msg), nil
		}
		if b, rerr := os.ReadFile(path); rerr == nil {
			oldContent = string(b)
			if oldContent == a.Content {
				return errResult(fmt.Sprintf("File %s already contains the exact content. No changes made.", path)), nil
			}
		}
	case !os.IsNotExist(err):
		return ToolResult{}, fmt.Errorf("error checking file: %w", err)
	}

	if !t.approve(ctx, FSRequest{SessionID: sessionID, CallID: callID, Tool: "write", Action: "write", Path: path,
		Description: "Create file " + path}) {
		return errResult(deniedText), nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ToolResult{}, fmt.Errorf("error creating directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(a.Content), 0o644); err != nil {
		return ToolResult{}, fmt.Errorf("error writing file: %w", err)
	}
	t.recordRead(sessionID, path)

	out := fmt.Sprintf("<result>\nFile successfully written: %s\n</result>", path)
	return okResult(out + t.after(ctx, sessionID, callID, path)), nil
}

func (t *fsTools) after(ctx context.Context, sessionID, callID, path string) string {
	if t.opts.AfterChange == nil {
		return ""
	}
	return t.opts.AfterChange(ctx, sessionID, callID, path)
}

// --- helpers -------------------------------------------------------------------

func parseArgs(argsJSON string, into any) (ToolResult, bool) {
	if strings.TrimSpace(argsJSON) == "" {
		argsJSON = "{}"
	}
	if err := json.Unmarshal([]byte(argsJSON), into); err != nil {
		return errResult("invalid arguments: " + err.Error()), true
	}
	return ToolResult{}, false
}

func objectSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
