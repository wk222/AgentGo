package coderuntime

// Small helpers and the wording the file tools use when they talk to the model.
// Every refusal and confirmation is defined here so the voice stays consistent
// and one read shows what a model can be told.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func okResult(s string) ToolResult  { return ToolResult{Content: s} }
func errResult(s string) ToolResult { return ToolResult{Content: s, IsError: true} }

// refusedByUser is returned when the approval hook says no.
const refusedByUser = "The user declined this request; nothing was done."

func parseArgs(argsJSON string, into any) (ToolResult, bool) {
	if strings.TrimSpace(argsJSON) == "" {
		argsJSON = "{}"
	}
	if err := json.Unmarshal([]byte(argsJSON), into); err != nil {
		return errResult("The arguments are not valid JSON for this tool: " + err.Error()), true
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

func schemaProp(kind, desc string) map[string]any {
	return map[string]any{"type": kind, "description": desc}
}

func strProp(desc string) map[string]any  { return schemaProp("string", desc) }
func intProp(desc string) map[string]any  { return schemaProp("integer", desc) }
func boolProp(desc string) map[string]any { return schemaProp("boolean", desc) }

// withDiagnostics appends language-server output (if any) under a confirmation.
func withDiagnostics(msg, diag string) string {
	if strings.TrimSpace(diag) == "" {
		return msg
	}
	return msg + "\n\n" + diag
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// --- refusals ----------------------------------------------------------------

func needFilePath() ToolResult { return errResult("file_path is required.") }

func isDirectory(path string) ToolResult {
	return errResult(path + " is a directory. Pass the path of a file inside it.")
}

func neverViewed(path string) ToolResult {
	return errResult("Refusing to change " + path + ": it has not been viewed in this session. " +
		"Run view on it first so the change is made against what is really there.")
}

func changedAfterView(path string, onDisk, viewed time.Time) ToolResult {
	return errResult(fmt.Sprintf("Refusing to change %s: it was modified after you last viewed it "+
		"(on disk %s, viewed %s). View it again, then retry.",
		path, onDisk.Format(time.RFC3339), viewed.Format(time.RFC3339)))
}

func alreadyExists(path string) ToolResult {
	return errResult(path + " already exists. To change it, view it and use an edit with a non-empty old_string.")
}

func missingFile(path string, near []string) ToolResult {
	if len(near) == 0 {
		return errResult("No such file: " + path)
	}
	return errResult("No such file: " + path + "\nFiles with a similar name:\n  " + strings.Join(near, "\n  "))
}

func noChange(path string) ToolResult {
	return errResult("Nothing to do: the requested change leaves " + path + " exactly as it is.")
}
