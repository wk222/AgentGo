package sessions

import (
	"agentgo/internal/promptbudget"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
)

// Which files earlier tool calls touched has to survive compaction.
//
// Dropping the history that says "write C:\work\report.docx" leaves the model
// with no record of artifacts it already produced, so it starts rebuilding them.
// A summary is prose and a list of paths is the first detail prose loses; the
// checkpoint's general ledger also shares one small budget with every tool
// result excerpt, so a few large results push older paths out. The paths are
// therefore kept mechanically, in their own bounded sets, and inherited by every
// later checkpoint.
//
// Like the rest of the checkpoint this records calls, not outcomes: a path here
// was named by a tool call, which does not show that the call succeeded.

// maxLedgerPaths bounds each set; the oldest entries are dropped first.
const maxLedgerPaths = 50

// FileLedger is the paths named by earlier tool calls, oldest first. A path
// that was both read and changed is reported only as modified.
type FileLedger struct {
	Read     []string `json:"read,omitempty"`
	Modified []string `json:"modified,omitempty"`
}

var fileMutation atomic.Pointer[func(string) bool]

// SetFileMutationLookup tells the ledger which tools change files. Tool
// declarations live above this package, so the assembly layer supplies them.
// The returned func restores the previous lookup.
func SetFileMutationLookup(fn func(string) bool) (restore func()) {
	prev := fileMutation.Swap(&fn)
	return func() { fileMutation.Store(prev) }
}

// Built-in file writers are recognised even before any lookup is installed.
var builtinFileWriters = map[string]bool{"write": true, "edit": true, "multiedit": true, "write_file": true, "edit_file": true, "apply_patch": true}

// Tools whose "path" is a search root or listing directory, not a file read.
var searchRootTools = map[string]bool{"grep": true, "glob": true, "ls": true, "list_dir": true, "find": true}

// Built-in coding tools reach the model as code_<name> ("code_write").
func bareToolName(name string) string { return strings.TrimPrefix(name, "code_") }

func toolChangesFiles(name string) bool {
	if builtinFileWriters[bareToolName(name)] {
		return true
	}
	if fn := fileMutation.Load(); fn != nil && *fn != nil {
		return (*fn)(name)
	}
	return false
}

func (l *FileLedger) Empty() bool { return l == nil || len(l.Read)+len(l.Modified) == 0 }

func (l *FileLedger) clone() *FileLedger {
	if l.Empty() {
		return nil
	}
	return &FileLedger{Read: append([]string(nil), l.Read...), Modified: append([]string(nil), l.Modified...)}
}

func without(list []string, path string) []string {
	out := list[:0:0]
	for _, p := range list {
		if p != path {
			out = append(out, p)
		}
	}
	return out
}

func recent(list []string) []string {
	if len(list) > maxLedgerPaths {
		return append([]string(nil), list[len(list)-maxLedgerPaths:]...)
	}
	return list
}

// note records one path. Seeing it again moves it to the newest position.
func (l *FileLedger) note(path string, modified bool) {
	path = strings.TrimSpace(path)
	if path == "" || len(path) > 400 {
		return
	}
	if modified {
		l.Read = without(l.Read, path)
		l.Modified = recent(append(without(l.Modified, path), path))
		return
	}
	for _, p := range l.Modified {
		if p == path {
			return
		}
	}
	l.Read = recent(append(without(l.Read, path), path))
}

// merge folds an older checkpoint's ledger into this one, in order.
func (l *FileLedger) merge(older *FileLedger) {
	if older.Empty() {
		return
	}
	for _, p := range older.Read {
		l.note(p, false)
	}
	for _, p := range older.Modified {
		l.note(p, true)
	}
}

// noteToolCall extracts the paths a call names and files them as read or
// modified. Unparseable arguments name no path.
func (l *FileLedger) noteToolCall(name, rawArgs string) {
	name = strings.TrimSpace(name)
	if name == "" || searchRootTools[bareToolName(name)] {
		return
	}
	var args any
	if json.Unmarshal([]byte(rawArgs), &args) != nil {
		return
	}
	modified := toolChangesFiles(name)
	for _, p := range pathsIn(args, 0) {
		l.note(p, modified)
	}
}

var ledgerPathKeys = map[string]bool{"path": true, "file_path": true, "relative_path": true, "file": true, "filename": true}

// pathsIn collects values of path-named fields, including those inside nested
// objects and lists such as multiedit's edits. Bounded in depth and count.
func pathsIn(v any, depth int) []string {
	if depth > 3 {
		return nil
	}
	var out []string
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if s, ok := val.(string); ok && ledgerPathKeys[strings.ToLower(k)] {
				out = append(out, s)
			} else if k == "paths" {
				if list, ok := val.([]any); ok {
					for _, item := range list {
						if s, ok := item.(string); ok {
							out = append(out, s)
						}
					}
				}
			} else {
				out = append(out, pathsIn(val, depth+1)...)
			}
		}
	case []any:
		for _, item := range t {
			out = append(out, pathsIn(item, depth+1)...)
		}
	}
	if len(out) > maxLedgerPaths {
		out = out[:maxLedgerPaths]
	}
	return out
}

// promptLines renders the ledger inside budget. Whole paths only: a shortened
// path names a different file. The newest paths are kept and the count of
// older ones left out is stated.
func (l *FileLedger) promptLines(budget int) []string {
	if l.Empty() || budget < 40 {
		return nil
	}
	half := budget
	if len(l.Read) > 0 && len(l.Modified) > 0 {
		half = budget / 2
	}
	var lines []string
	section := func(label string, paths []string, limit int) {
		if len(paths) == 0 {
			return
		}
		kept := 0
		used := promptbudget.Estimate(label + " (+999 older omitted): ")
		var picked []string
		for i := len(paths) - 1; i >= 0; i-- {
			cost := promptbudget.Estimate(paths[i]) + 1
			if used+cost > limit {
				break
			}
			used += cost
			picked = append(picked, paths[i])
			kept++
		}
		if kept == 0 {
			return
		}
		for i, j := 0, len(picked)-1; i < j; i, j = i+1, j-1 {
			picked[i], picked[j] = picked[j], picked[i]
		}
		line := label
		if omitted := len(paths) - kept; omitted > 0 {
			line += " (+" + strconv.Itoa(omitted) + " older omitted)"
		}
		lines = append(lines, line+": "+strings.Join(picked, "; "))
	}
	section("Files named in earlier modifying tool calls (not verified)", l.Modified, half)
	section("Files named in earlier read tool calls", l.Read, half)
	return lines
}
