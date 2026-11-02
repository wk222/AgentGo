package coderuntime

// edit and multiedit: exact find-and-replace on files the session has viewed.

import (
	"context"
	"fmt"
	"os"
	"strings"
)

type editArgs struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

type multiEditOp struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

type multiEditArgs struct {
	FilePath string        `json:"file_path"`
	Edits    []multiEditOp `json:"edits"`
}

func (t *fsTools) editTool() Tool {
	return Tool{
		Name: "edit",
		Description: "Change a file by swapping one piece of text for another. old_string must be found exactly once unless replace_all is true. " +
			"An empty old_string creates a new file from new_string; an empty new_string deletes the matched text. " +
			"When old_string differs from the file only in whitespace or typographic punctuation (curly quotes, long dashes), " +
			"the matching lines are still replaced and new_string is re-indented to fit; the reply says when that happened, so check the result. " +
			"View the file first. For several changes to one file, use multiedit.",
		Schema: objectSchema(map[string]any{
			"file_path":   strProp("Path of the file to change"),
			"old_string":  strProp("The text to find"),
			"new_string":  strProp("The text to put in its place"),
			"replace_all": boolProp("Replace every occurrence of old_string instead of requiring exactly one (default false)"),
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

func (t *fsTools) multiEditTool() Tool {
	op := objectSchema(map[string]any{
		"old_string":  strProp("The text to find"),
		"new_string":  strProp("The text to put in its place"),
		"replace_all": boolProp("Replace every occurrence of old_string (default false)"),
	}, "old_string", "new_string")
	return Tool{
		Name: "multiedit",
		Description: "Make several find-and-replace changes to one file in a single call. They run in the order given and each one sees the result of the one before. " +
			"An edit that cannot be applied is reported and skipped while the others still go through. " +
			"Only the first edit may leave old_string empty; that creates the file. View the file first.",
		Schema: objectSchema(map[string]any{
			"file_path": strProp("Path of the file to change"),
			"edits": map[string]any{
				"type": "array", "items": op,
				"description": "The changes, applied top to bottom",
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

// --- loading the file for a change --------------------------------------------

// editable is a file read for modification, with LF line endings.
type editable struct {
	text string
	crlf bool
	raw  []byte
}

// loadEditable stats and reads path for a modification, after the viewed check.
func (t *fsTools) loadEditable(sessionID, path string) (*editable, *ToolResult, error) {
	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		r := missingFile(path, nil)
		return nil, &r, nil
	case err != nil:
		return nil, nil, fmt.Errorf("cannot stat %s: %w", path, err)
	case info.IsDir():
		r := isDirectory(path)
		return nil, &r, nil
	}
	if r := t.verifyFresh(sessionID, path, info); r != nil {
		return nil, r, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	text, crlf := splitEOL(string(raw))
	return &editable{text: text, crlf: crlf, raw: raw}, nil, nil
}

// store writes the edited text back, restoring CRLF endings if the file had them.
func (t *fsTools) store(ctx context.Context, sessionID, callID, tool, path, desc string, f *editable, text string) (note string, refusal *ToolResult, err error) {
	if f.crlf {
		text = joinEOL(text)
	}
	return t.persist(ctx, sessionID, callID, change{
		tool: tool, path: path, desc: desc,
		before: f.raw, existed: true, readOK: true, after: []byte(text),
	})
}

// createNew writes a file that must not exist yet.
func (t *fsTools) createNew(ctx context.Context, sessionID, callID, tool, path, content string) (refusal *ToolResult, err error) {
	switch info, serr := os.Stat(path); {
	case serr == nil && info.IsDir():
		r := isDirectory(path)
		return &r, nil
	case serr == nil:
		r := alreadyExists(path)
		return &r, nil
	case !os.IsNotExist(serr):
		return nil, fmt.Errorf("cannot stat %s: %w", path, serr)
	}
	_, refusal, err = t.persist(ctx, sessionID, callID, change{
		tool: tool, path: path, desc: "Create file " + path, after: []byte(content),
	})
	return refusal, err
}

// --- edit ----------------------------------------------------------------------

func (t *fsTools) edit(ctx context.Context, sessionID, callID string, a editArgs) (ToolResult, error) {
	if a.FilePath == "" {
		return needFilePath(), nil
	}
	path := t.resolve(a.FilePath)

	res, err := t.withPathLock(ctx, path, func() (ToolResult, error) {
		if a.OldString == "" {
			if refusal, err := t.createNew(ctx, sessionID, callID, "edit", path, a.NewString); refusal != nil || err != nil {
				if refusal != nil {
					return *refusal, nil
				}
				return ToolResult{}, err
			}
			return okResult("Created " + path), nil
		}
		return t.replaceText(ctx, sessionID, callID, path, a)
	})
	if err != nil || res.IsError {
		return res, err
	}
	return okResult(withDiagnostics(res.Content, t.afterChange(ctx, sessionID, callID, path))), nil
}

func (t *fsTools) replaceText(ctx context.Context, sessionID, callID, path string, a editArgs) (ToolResult, error) {
	f, refusal, err := t.loadEditable(sessionID, path)
	if refusal != nil || err != nil {
		if refusal != nil {
			return *refusal, nil
		}
		return ToolResult{}, err
	}
	got, aerr := applyEdit(f.text, a.OldString, a.NewString, a.ReplaceAll)
	if aerr != nil {
		return errResult(aerr.Error()), nil
	}
	if got.Content == f.text {
		return noChange(path), nil
	}

	desc, msg := "Edit file "+path, fmt.Sprintf("Edited %s: replaced %s.", path, plural(got.Count, "occurrence", "occurrences"))
	if a.NewString == "" {
		desc, msg = "Delete text from file "+path, fmt.Sprintf("Edited %s: removed %s.", path, plural(got.Count, "occurrence", "occurrences"))
	}
	note, refusal, err := t.store(ctx, sessionID, callID, "edit", path, desc, f, got.Content)
	if refusal != nil || err != nil {
		if refusal != nil {
			return *refusal, nil
		}
		return ToolResult{}, err
	}
	return okResult(withToleranceNote(msg, got.Rung) + note), nil
}

// --- multiedit -------------------------------------------------------------------

func (t *fsTools) multiEdit(ctx context.Context, sessionID, callID string, a multiEditArgs) (ToolResult, error) {
	if a.FilePath == "" {
		return needFilePath(), nil
	}
	if len(a.Edits) == 0 {
		return errResult("edits is empty; give at least one change."), nil
	}
	for i, e := range a.Edits[1:] {
		if e.OldString == "" {
			return errResult(fmt.Sprintf("edit %d has an empty old_string; only the first edit may, to create the file.", i+2)), nil
		}
	}
	path := t.resolve(a.FilePath)

	res, err := t.withPathLock(ctx, path, func() (ToolResult, error) {
		if a.Edits[0].OldString == "" {
			return t.multiCreate(ctx, sessionID, callID, path, a.Edits)
		}
		return t.multiModify(ctx, sessionID, callID, path, a.Edits)
	})
	if err != nil || res.IsError {
		return res, err
	}
	return okResult(withDiagnostics(res.Content, t.afterChange(ctx, sessionID, callID, path))), nil
}

type editFailure struct {
	n   int // 1-based position in the request
	why string
}

// runEdits applies ops in order to text. A failing op is recorded and skipped.
// first is the position number of ops[0]; rung is the loosest comparison any op
// needed (0 when every op matched literally).
func runEdits(text string, ops []multiEditOp, first int) (out string, failed []editFailure, rung int) {
	out = text
	for i, op := range ops {
		if op.OldString == "" && op.NewString == "" {
			continue
		}
		got, err := applyEdit(out, op.OldString, op.NewString, op.ReplaceAll)
		if err != nil {
			failed = append(failed, editFailure{n: first + i, why: err.Error()})
			continue
		}
		rung = max(rung, got.Rung)
		out = got.Content
	}
	return out, failed, rung
}

func appendFailures(r ToolResult, failed []editFailure) ToolResult {
	var b strings.Builder
	b.WriteString(r.Content)
	for _, f := range failed {
		fmt.Fprintf(&b, "\n  edit %d not applied: %s", f.n, f.why)
	}
	r.Content = b.String()
	return r
}

func (t *fsTools) multiCreate(ctx context.Context, sessionID, callID, path string, ops []multiEditOp) (ToolResult, error) {
	// The first op's new_string is the seed; the rest edit it.
	text, failed, rung := runEdits(ops[0].NewString, ops[1:], 2)
	if refusal, err := t.createNew(ctx, sessionID, callID, "multiedit", path, text); refusal != nil || err != nil {
		if refusal != nil {
			return *refusal, nil
		}
		return ToolResult{}, err
	}
	msg := fmt.Sprintf("Created %s from %d edits.", path, len(ops))
	if len(failed) > 0 {
		msg = fmt.Sprintf("Created %s: %d of %d edits applied.", path, len(ops)-len(failed), len(ops))
	}
	return appendFailures(okResult(withToleranceNote(msg, rung)), failed), nil
}

func (t *fsTools) multiModify(ctx context.Context, sessionID, callID, path string, ops []multiEditOp) (ToolResult, error) {
	f, refusal, err := t.loadEditable(sessionID, path)
	if refusal != nil || err != nil {
		if refusal != nil {
			return *refusal, nil
		}
		return ToolResult{}, err
	}
	text, failed, rung := runEdits(f.text, ops, 1)
	if text == f.text {
		if len(failed) > 0 {
			return appendFailures(errResult(fmt.Sprintf("None of the %d edits could be applied; %s is unchanged.", len(ops), path)), failed), nil
		}
		return noChange(path), nil
	}

	applied := len(ops) - len(failed)
	desc := fmt.Sprintf("Apply %d edits to file %s", applied, path)
	msg := fmt.Sprintf("Edited %s: all %d edits applied.", path, len(ops))
	if len(failed) > 0 {
		desc = fmt.Sprintf("Apply %d of %d edits to file %s", applied, len(ops), path)
		msg = fmt.Sprintf("Edited %s: %d of %d edits applied.", path, applied, len(ops))
	}
	note, refusal, err := t.store(ctx, sessionID, callID, "multiedit", path, desc, f, text)
	if refusal != nil || err != nil {
		if refusal != nil {
			return *refusal, nil
		}
		return ToolResult{}, err
	}
	return appendFailures(okResult(withToleranceNote(msg, rung)+note), failed), nil
}
