package coderuntime

import (
	"context"
	"fmt"
	"strings"
)

// file_changes and revert_file_changes: the model-facing side of the journal.
// They exist for one purpose, the user asking to undo ("/undo", "撤销刚才的修改"):
// the model must not use them to cover its own tracks or to "start over" unasked,
// and the descriptions say so. revert_file_changes is a file-changing tool, so the
// usual approval applies.

type changesArgs struct {
	CallID string `json:"call_id"`
}

func (t *fsTools) changesTool() Tool {
	return Tool{
		Name: "file_changes",
		Description: "List the files this session's write/edit/multiedit calls changed, oldest first, each with its call_id and whether it is already reverted. " +
			"Use it when the user asks what was changed or wants to undo something.",
		Schema:   objectSchema(map[string]any{}),
		Parallel: true,
		Run: func(ctx context.Context, sessionID, callID, argsJSON string) (ToolResult, error) {
			cs := t.opts.Journal.List(sessionID)
			if len(cs) == 0 {
				return okResult("No file changes are recorded for this session."), nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%d recorded change(s), oldest first:\n", len(cs))
			for _, c := range cs {
				state := ""
				if c.Reverted {
					state = " [reverted]"
				}
				action := "modified"
				if !c.Existed {
					action = "created"
				}
				fmt.Fprintf(&b, "- call_id=%s %s %s %s%s\n", c.CallID, c.Tool, action, c.Path, state)
			}
			return okResult(b.String()), nil
		},
	}
}

func (t *fsTools) revertTool() Tool {
	return Tool{
		Name: "revert_file_changes",
		Description: "Undo file changes made earlier in this session by write/edit/multiedit, newest first, putting back what each file held before (and deleting files the agent created). " +
			"call_id: roll back from that call onward (see code_file_changes); empty means only the latest call; \"all\" means everything in the session. " +
			"A file the user changed after the agent wrote it is left alone and reported. " +
			"Only call this when the user asked to undo; never to discard your own work unasked.",
		Schema: objectSchema(map[string]any{
			"call_id": strProp("Roll back from this call onward; empty = only the latest call; \"all\" = the whole session"),
		}),
		Run: func(ctx context.Context, sessionID, callID, argsJSON string) (ToolResult, error) {
			var a changesArgs
			if r, bad := parseArgs(argsJSON, &a); bad {
				return r, nil
			}
			target := strings.TrimSpace(a.CallID)
			if !t.approve(ctx, FSRequest{SessionID: sessionID, CallID: callID, Tool: "revert_file_changes", Action: "write",
				Path: t.root, Description: "Undo this session's file changes (" + describeTarget(target) + ")"}) {
				return errResult(deniedText), nil
			}
			rep, err := t.opts.Journal.Revert(sessionID, target)
			if err != nil {
				return errResult(err.Error()), nil
			}
			return okResult(rep.Summary()), nil
		},
	}
}

func describeTarget(target string) string {
	switch target {
	case "":
		return "the latest call"
	case "all":
		return "everything"
	}
	return "from call " + target
}
