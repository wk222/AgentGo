// The edit/multiedit contract in this file (parameters and the model-visible
// messages) is derived from Crush's edit and multiedit tools
// (licenses/crush/NOTICE.md, FSL-1.1-MIT).

package coderuntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// --- edit ----------------------------------------------------------------------

type editArgs struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

func (t *fsTools) editTool() Tool {
	return Tool{
		Name: "edit",
		Description: "Edit a file by exact find-and-replace; it can also create a file (empty old_string) or delete text (empty new_string). " +
			"old_string must match exactly once unless replace_all is set. If it differs from the file only in whitespace or typographic punctuation (curly quotes, dashes), " +
			"the matching lines are still edited and new_string is re-indented to fit; the response says so, so verify the result. " +
			"The file must have been viewed first. For several changes to one file use multiedit.",
		Schema: objectSchema(map[string]any{
			"file_path":   strProp("The absolute path to the file to modify"),
			"old_string":  strProp("The text to replace"),
			"new_string":  strProp("The text to replace it with"),
			"replace_all": boolProp("Replace all occurrences of old_string (default false)"),
		}, "file_path", "old_string", "new_string"),
		Run: func(ctx context.Context, sessionID, callID, argsJSON string) (ToolResult, error) {
			var a editArgs
			if r, bad := parseArgs(argsJSON, &a); bad {
				return r, nil
			}
			return t.edit(ctx, sessionID, callID, a)
		},
	}
}

func (t *fsTools) edit(ctx context.Context, sessionID, callID string, a editArgs) (ToolResult, error) {
	if a.FilePath == "" {
		return errResult("file_path is required"), nil
	}
	path := t.resolve(a.FilePath)

	// Held for the read-check-approve-write sequence only, so a queue of edits to
	// one file does not wait on each other's diagnostics in after().
	res, err := t.withPathLock(ctx, path, func() (ToolResult, error) {
		if a.OldString == "" {
			return t.createFile(ctx, sessionID, callID, "edit", path, a.NewString, "File created: "+path)
		}
		return t.replaceInFile(ctx, sessionID, callID, "edit", path, a)
	})
	if err != nil || res.IsError {
		return res, err
	}
	text := fmt.Sprintf("<result>\n%s\n</result>\n", res.Content)
	return okResult(text + t.after(ctx, sessionID, callID, path)), nil
}

// createFile writes a brand-new file; it refuses to touch an existing one.
func (t *fsTools) createFile(ctx context.Context, sessionID, callID, tool, path, content, message string) (ToolResult, error) {
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return errResult("path is a directory, not a file: " + path), nil
		}
		return errResult("file already exists: " + path), nil
	} else if !os.IsNotExist(err) {
		return ToolResult{}, fmt.Errorf("failed to access file: %w", err)
	}
	if !t.approve(ctx, FSRequest{SessionID: sessionID, CallID: callID, Tool: tool, Action: "write", Path: path,
		Description: "Create file " + path}) {
		return errResult(deniedText), nil
	}
	if bad := t.guardContent(path, "", content); bad != nil {
		return *bad, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ToolResult{}, fmt.Errorf("failed to create parent directories: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return ToolResult{}, fmt.Errorf("failed to write file: %w", err)
	}
	t.journal(sessionID, callID, tool, path, nil, false, false, []byte(content))
	t.recordRead(sessionID, path)
	return okResult(message), nil
}

// loadExisting reads a file for modification, enforcing read-before-edit, and
// returns its content with LF line endings plus whether it was CRLF.
func (t *fsTools) loadExisting(sessionID, path string) (content string, crlf bool, bad *ToolResult, err error) {
	info, serr := os.Stat(path)
	if serr != nil {
		if os.IsNotExist(serr) {
			r := errResult("file not found: " + path)
			return "", false, &r, nil
		}
		return "", false, nil, fmt.Errorf("failed to access file: %w", serr)
	}
	if info.IsDir() {
		r := errResult("path is a directory, not a file: " + path)
		return "", false, &r, nil
	}
	if msg := t.staleCheck(sessionID, path, info, styleEdit); msg != "" {
		r := errResult(msg)
		return "", false, &r, nil
	}
	b, rerr := os.ReadFile(path)
	if rerr != nil {
		return "", false, nil, fmt.Errorf("failed to read file: %w", rerr)
	}
	content, crlf = toUnixLineEndings(string(b))
	return content, crlf, nil, nil
}

// commit asks for approval, writes (restoring CRLF) and records the read.
func (t *fsTools) commit(ctx context.Context, sessionID, callID, tool, path, desc, newContent string, crlf bool) (*ToolResult, string, error) {
	var prior string
	priorRead := false
	if b, err := os.ReadFile(path); err == nil {
		prior, priorRead = string(b), true
	}
	if bad := t.guardContent(path, prior, newContent); bad != nil {
		return bad, "", nil
	}
	if !t.approve(ctx, FSRequest{SessionID: sessionID, CallID: callID, Tool: tool, Action: "write", Path: path, Description: desc}) {
		r := errResult(deniedText)
		return &r, "", nil
	}
	if crlf {
		newContent = toWindowsLineEndings(newContent)
	}
	backup := t.backupBeforeOverwrite(path)
	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		return nil, "", fmt.Errorf("failed to write file: %w", err)
	}
	t.journal(sessionID, callID, tool, path, []byte(prior), true, priorRead, []byte(newContent))
	t.recordRead(sessionID, path)
	return nil, backupNote(backup), nil
}

func (t *fsTools) replaceInFile(ctx context.Context, sessionID, callID, tool, path string, a editArgs) (ToolResult, error) {
	old, crlf, bad, err := t.loadExisting(sessionID, path)
	if err != nil {
		return ToolResult{}, err
	}
	if bad != nil {
		return *bad, nil
	}
	applied, ferr := applyEdit(old, a.OldString, a.NewString, a.ReplaceAll)
	if ferr != nil {
		return errResult(ferr.Error()), nil
	}
	result := applied.Content
	deleting := a.NewString == ""
	if !deleting && result == old {
		return errResult("new content is the same as old content. No changes made."), nil
	}
	desc, msg := "Replace content in file "+path, "Content replaced in file: "+path
	if deleting {
		desc, msg = "Delete content from file "+path, "Content deleted from file: "+path
	}
	denied, note, err := t.commit(ctx, sessionID, callID, tool, path, desc, result, crlf)
	if err != nil || denied != nil {
		if denied != nil {
			return *denied, nil
		}
		return ToolResult{}, err
	}
	return okResult(withToleranceNote(msg, applied.Rung) + note), nil
}

// --- multiedit -------------------------------------------------------------------

type multiEditOp struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

type multiEditArgs struct {
	FilePath string        `json:"file_path"`
	Edits    []multiEditOp `json:"edits"`
}

type failedEdit struct {
	Index int
	Error string
}

func (t *fsTools) multiEditTool() Tool {
	op := objectSchema(map[string]any{
		"old_string":  strProp("The text to replace"),
		"new_string":  strProp("The text to replace it with"),
		"replace_all": boolProp("Replace all occurrences of old_string (default false)."),
	}, "old_string", "new_string")
	return Tool{
		Name: "multiedit",
		Description: "Apply several find-and-replace edits to one file in order, in a single call. Each edit sees the result of the previous one. " +
			"Edits that fail are reported and skipped; the rest are still applied. " +
			"Only the first edit may have an empty old_string, which creates the file. The file must have been viewed first.",
		Schema: objectSchema(map[string]any{
			"file_path": strProp("The absolute path to the file to modify"),
			"edits": map[string]any{
				"type": "array", "items": op,
				"description": "Array of edit operations to perform sequentially on the file",
			},
		}, "file_path", "edits"),
		Run: func(ctx context.Context, sessionID, callID, argsJSON string) (ToolResult, error) {
			var a multiEditArgs
			if r, bad := parseArgs(argsJSON, &a); bad {
				return r, nil
			}
			return t.multiEdit(ctx, sessionID, callID, a)
		},
	}
}

func (t *fsTools) multiEdit(ctx context.Context, sessionID, callID string, a multiEditArgs) (ToolResult, error) {
	if a.FilePath == "" {
		return errResult("file_path is required"), nil
	}
	if len(a.Edits) == 0 {
		return errResult("at least one edit operation is required"), nil
	}
	for i, e := range a.Edits {
		if i > 0 && e.OldString == "" {
			return errResult(fmt.Sprintf("edit %d: only the first edit can have empty old_string (for file creation)", i+1)), nil
		}
	}
	path := t.resolve(a.FilePath)

	res, err := t.withPathLock(ctx, path, func() (ToolResult, error) {
		if a.Edits[0].OldString == "" {
			return t.multiEditCreate(ctx, sessionID, callID, path, a.Edits)
		}
		return t.multiEditExisting(ctx, sessionID, callID, path, a.Edits)
	})
	if err != nil || res.IsError {
		return res, err
	}
	text := fmt.Sprintf("<result>\n%s\n</result>\n", res.Content)
	return okResult(text + t.after(ctx, sessionID, callID, path)), nil
}

// applyEdits runs edits in order; failures are collected, not fatal.
// The returned rung is the loosest comparison any edit needed (0 = all literal).
func applyEdits(content string, edits []multiEditOp, startIndex int) (string, []failedEdit, int) {
	var failed []failedEdit
	rung := 0
	for i, e := range edits {
		if e.OldString == "" && e.NewString == "" {
			continue
		}
		applied, err := applyEdit(content, e.OldString, e.NewString, e.ReplaceAll)
		if err != nil {
			failed = append(failed, failedEdit{Index: startIndex + i + 1, Error: err.Error()})
			continue
		}
		rung = max(rung, applied.Rung)
		content = applied.Content
	}
	return content, failed, rung
}

func (t *fsTools) multiEditCreate(ctx context.Context, sessionID, callID, path string, edits []multiEditOp) (ToolResult, error) {
	content, failed, rung := applyEdits(edits[0].NewString, edits[1:], 1)
	applied := len(edits) - len(failed)
	msg := fmt.Sprintf("File created with %d edits: %s", len(edits), path)
	if len(failed) > 0 {
		msg = fmt.Sprintf("File created with %d of %d edits: %s (%d edit(s) failed)", applied, len(edits), path, len(failed))
	}
	res, err := t.createFile(ctx, sessionID, callID, "multiedit", path, content, withToleranceNote(msg, rung))
	return withFailures(res, failed), err
}

func (t *fsTools) multiEditExisting(ctx context.Context, sessionID, callID, path string, edits []multiEditOp) (ToolResult, error) {
	old, crlf, bad, err := t.loadExisting(sessionID, path)
	if err != nil {
		return ToolResult{}, err
	}
	if bad != nil {
		return *bad, nil
	}
	content, failed, rung := applyEdits(old, edits, 0)
	if content == old {
		if len(failed) > 0 {
			return withFailures(errResult(fmt.Sprintf("no changes made - all %d edit(s) failed", len(failed))), failed), nil
		}
		return errResult("no changes made - all edits resulted in identical content"), nil
	}
	applied := len(edits) - len(failed)
	desc := fmt.Sprintf("Apply %d edits to file %s", applied, path)
	msg := fmt.Sprintf("Applied %d edits to file: %s", len(edits), path)
	if len(failed) > 0 {
		desc = fmt.Sprintf("Apply %d of %d edits to file %s (%d failed)", applied, len(edits), path, len(failed))
		msg = fmt.Sprintf("Applied %d of %d edits to file: %s (%d edit(s) failed)", applied, len(edits), path, len(failed))
	}
	denied, note, err := t.commit(ctx, sessionID, callID, "multiedit", path, desc, content, crlf)
	if err != nil || denied != nil {
		if denied != nil {
			return *denied, nil
		}
		return ToolResult{}, err
	}
	return withFailures(okResult(withToleranceNote(msg, rung)+note), failed), nil
}

// withFailures lists failed edits after the message so the model can retry
// exactly those. (The Crush tool put them in metadata the model never saw.)
func withFailures(r ToolResult, failed []failedEdit) ToolResult {
	if len(failed) == 0 {
		return r
	}
	for _, f := range failed {
		r.Content += fmt.Sprintf("\nedit %d failed: %s", f.Index, f.Error)
	}
	return r
}
