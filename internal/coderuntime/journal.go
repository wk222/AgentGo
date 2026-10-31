package coderuntime

// The change journal: what the file tools changed in a session, and a way back.
//
// pre-overwrite backups (fs_guard.go) keep a copy of a file the agent replaced,
// but nothing says which copy belongs to which step, a created file leaves
// nothing to restore, and a file git can restore is skipped altogether. The
// journal records every successful write, edit and multiedit with the bytes
// that were there before, so "undo what the last step did" and "go back to
// before step N" are one operation. It only ever puts back what the tools
// wrote: a file the user changed afterwards is reported, never overwritten.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// maxJournalBytes is the largest "before" copy the journal keeps for one change.
	maxJournalBytes = 20 << 20
	// maxJournalChanges bounds one session's journal; the oldest changes drop first.
	maxJournalChanges = 300
)

// Change is one successful file change made by a tool call.
type Change struct {
	Seq       int       `json:"seq"`
	SessionID string    `json:"session_id"`
	CallID    string    `json:"call_id"`
	Tool      string    `json:"tool"`
	Path      string    `json:"path"`
	Existed   bool      `json:"existed"`   // the file was there before the change
	Saved     bool      `json:"saved"`     // its previous bytes were kept (always true for a create)
	AfterSHA  string    `json:"after_sha"` // sha256 of what the tool wrote
	Time      time.Time `json:"time"`
	Reverted  bool      `json:"reverted,omitempty"` // already put back
}

// RevertReport says what a revert did, path by path.
type RevertReport struct {
	CallID    string   `json:"call_id,omitempty"` // the first call that was rolled back
	Restored  []string `json:"restored,omitempty"`
	Removed   []string `json:"removed,omitempty"`   // files the agent had created
	Conflicts []string `json:"conflicts,omitempty"` // changed since the agent wrote them: left alone
	Unsaved   []string `json:"unsaved,omitempty"`   // too large to have been kept: cannot be restored
}

// Journal is the change journal of one workspace, kept under
// <workspace>/.agentgo/changes/<session>/. Use OpenJournal.
type Journal struct {
	dir string
	mu  sync.Mutex
}

var (
	journalsMu sync.Mutex
	journals   = map[string]*Journal{}
)

// OpenJournal returns the journal of workDir; every caller in the process shares one.
func OpenJournal(workDir string) *Journal {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		abs = workDir
	}
	dir := filepath.Join(abs, ".agentgo", "changes")
	key := lockKey(dir)
	journalsMu.Lock()
	defer journalsMu.Unlock()
	if j := journals[key]; j != nil {
		return j
	}
	j := &Journal{dir: dir}
	journals[key] = j
	return j
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func (j *Journal) sessionDir(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	name := unsafeName.ReplaceAllString(sessionID, "_")
	if len(name) > 40 {
		name = name[:40]
	}
	return filepath.Join(j.dir, name+"-"+hex.EncodeToString(sum[:3]))
}

func shaHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func (j *Journal) load(dir string) []Change {
	raw, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		return nil
	}
	var cs []Change
	if json.Unmarshal(raw, &cs) != nil {
		return nil
	}
	return cs
}

func (j *Journal) save(dir string, cs []Change) error {
	raw, err := json.Marshal(cs)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "index.json.tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "index.json"))
}

// Record notes a successful change: before is what the file held (ignored when
// existed is false) and after is what the tool wrote. Best effort, it never
// fails the write it describes.
func (j *Journal) Record(sessionID, callID, tool, path string, before []byte, existed bool, after []byte) {
	j.recordChange(sessionID, callID, tool, path, before, existed, true, after)
}

// recordChange is Record; keep false notes a change whose previous bytes could not be read.
func (j *Journal) recordChange(sessionID, callID, tool, path string, before []byte, existed, keep bool, after []byte) {
	if j == nil || sessionID == "" {
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	dir := j.sessionDir(sessionID)
	cs := j.load(dir)
	seq := 1
	if n := len(cs); n > 0 {
		seq = cs[n-1].Seq + 1
	}
	c := Change{Seq: seq, SessionID: sessionID, CallID: callID, Tool: tool, Path: abs, Existed: existed, Saved: true,
		AfterSHA: shaHex(after), Time: time.Now()}
	if existed {
		if !keep || len(before) > maxJournalBytes {
			c.Saved = false
		} else if err := os.MkdirAll(dir, 0o755); err != nil || os.WriteFile(filepath.Join(dir, strconv.Itoa(seq)+".before"), before, 0o600) != nil {
			c.Saved = false
		}
	}
	cs = append(cs, c)
	for len(cs) > maxJournalChanges {
		_ = os.Remove(filepath.Join(dir, strconv.Itoa(cs[0].Seq)+".before"))
		cs = cs[1:]
	}
	_ = j.save(dir, cs)
}

// List returns the session's changes, oldest first.
func (j *Journal) List(sessionID string) []Change {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.load(j.sessionDir(sessionID))
}

// Revert puts files back, newest change first, from the first change of callID
// onward. callID "" means the latest call only; "all" means everything not yet
// reverted. A file whose current bytes are not what the tool wrote is a
// conflict and is left as it is, and so is everything older that depended on it.
func (j *Journal) Revert(sessionID, callID string) (RevertReport, error) {
	var rep RevertReport
	if j == nil {
		return rep, fmt.Errorf("the change journal is off")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	dir := j.sessionDir(sessionID)
	cs := j.load(dir)
	var live []int // indexes into cs of changes not yet reverted
	for i, c := range cs {
		if !c.Reverted {
			live = append(live, i)
		}
	}
	if len(live) == 0 {
		return rep, fmt.Errorf("nothing to revert: this session has no file changes left to undo")
	}
	from := 0
	switch callID {
	case "all":
		rep.CallID = cs[live[0]].CallID
	case "":
		last := cs[live[len(live)-1]].CallID
		from = len(live) - 1
		for from > 0 && cs[live[from-1]].CallID == last {
			from--
		}
		rep.CallID = last
	default:
		from = -1
		for k, i := range live {
			if cs[i].CallID == callID {
				from = k
				break
			}
		}
		if from < 0 {
			return rep, fmt.Errorf("call %q made no file change that can still be undone; use code_file_changes to list them", callID)
		}
		rep.CallID = callID
	}
	for k := len(live) - 1; k >= from; k-- {
		c := &cs[live[k]]
		cur, rerr := os.ReadFile(c.Path)
		switch {
		case rerr == nil && shaHex(cur) != c.AfterSHA:
			rep.Conflicts = append(rep.Conflicts, c.Path)
			continue
		case rerr != nil && !os.IsNotExist(rerr):
			rep.Conflicts = append(rep.Conflicts, c.Path)
			continue
		}
		if !c.Existed {
			if rerr == nil {
				if err := os.Remove(c.Path); err != nil {
					rep.Conflicts = append(rep.Conflicts, c.Path)
					continue
				}
			}
			c.Reverted = true
			rep.Removed = append(rep.Removed, c.Path)
			continue
		}
		if !c.Saved {
			rep.Unsaved = append(rep.Unsaved, c.Path)
			continue
		}
		before, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(c.Seq)+".before"))
		if err != nil {
			rep.Unsaved = append(rep.Unsaved, c.Path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil || os.WriteFile(c.Path, before, 0o644) != nil {
			rep.Conflicts = append(rep.Conflicts, c.Path)
			continue
		}
		c.Reverted = true
		rep.Restored = append(rep.Restored, c.Path)
	}
	if err := j.save(dir, cs); err != nil {
		return rep, fmt.Errorf("files were put back but the journal could not be updated: %w", err)
	}
	return rep, nil
}

// Summary renders a report for the model and the user.
func (r RevertReport) Summary() string {
	var b strings.Builder
	list := func(title string, paths []string) {
		if len(paths) == 0 {
			return
		}
		sort.Strings(paths)
		fmt.Fprintf(&b, "%s (%d):\n", title, len(paths))
		for _, p := range paths {
			b.WriteString("  " + p + "\n")
		}
	}
	list("Restored to the version before the change", r.Restored)
	list("Deleted (the agent had created them)", r.Removed)
	list("Left alone: changed since the agent wrote them, undo would lose that work", r.Conflicts)
	list("Cannot be restored: the previous version was too large to keep", r.Unsaved)
	if b.Len() == 0 {
		return "Nothing was changed."
	}
	return strings.TrimRight(b.String(), "\n")
}
