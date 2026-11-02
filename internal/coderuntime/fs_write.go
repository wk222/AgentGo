package coderuntime

// write: create a file or replace one wholesale.

import (
	"context"
	"fmt"
	"os"
)

type writeArgs struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func (t *fsTools) writeTool() Tool {
	return Tool{
		Name: "write",
		Description: "Create a file with exactly `content`, or replace an existing file's whole contents; missing parent folders are created. " +
			"Replacing a file requires that you viewed it in this session and that it has not changed since. " +
			"To alter part of a file, use edit or multiedit instead; keep write for new files and complete rewrites.",
		Schema: objectSchema(map[string]any{
			"file_path": strProp("Path of the file to create or replace"),
			"content":   strProp("The full text the file should contain"),
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
		return needFilePath(), nil
	}
	path := t.resolve(a.FilePath)

	// The lock covers the checks and the write, not the diagnostics afterwards,
	// so queued writes to one file do not wait on a language server.
	res, err := t.withPathLock(ctx, path, func() (ToolResult, error) {
		return t.replaceWhole(ctx, sessionID, callID, path, a.Content)
	})
	if err != nil || res.IsError {
		return res, err
	}
	return okResult(withDiagnostics(res.Content, t.afterChange(ctx, sessionID, callID, path))), nil
}

// replaceWhole performs the checks and the write; on success the result holds
// the confirmation line.
func (t *fsTools) replaceWhole(ctx context.Context, sessionID, callID, path, content string) (ToolResult, error) {
	c := change{tool: "write", path: path, after: []byte(content), desc: "Create file " + path}

	info, err := os.Stat(path)
	switch {
	case err == nil:
		if info.IsDir() {
			return isDirectory(path), nil
		}
		if r := t.verifyFresh(sessionID, path, info); r != nil {
			return *r, nil
		}
		c.existed = true
		c.desc = "Overwrite file " + path
		if prev, rerr := os.ReadFile(path); rerr == nil {
			c.before, c.readOK = prev, true
			if string(prev) == content {
				return errResult(path + " already holds exactly this content; nothing written."), nil
			}
		}
	case !os.IsNotExist(err):
		return ToolResult{}, fmt.Errorf("cannot stat %s: %w", path, err)
	}

	note, refusal, err := t.persist(ctx, sessionID, callID, c)
	if err != nil || refusal != nil {
		if refusal != nil {
			return *refusal, nil
		}
		return ToolResult{}, err
	}
	verb := "Created"
	if c.existed {
		verb = "Replaced"
	}
	return okResult(fmt.Sprintf("%s %s (%d bytes)%s", verb, path, len(content), note)), nil
}
