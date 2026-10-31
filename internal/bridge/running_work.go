package bridge

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"agentgo/internal/agent"
	"agentgo/internal/core"
	"agentgo/internal/engine"
	"agentgo/internal/taskhub"
)

// What the app is doing for the user right now, and what is waiting for them, in
// one answer. No single place knows it all:
//
//   - the agent runner's cancel handles cover every conversation turn that is at
//     work, including the segments that carry on after an approval or an answer;
//   - the engine router covers runs started through RunEngine, but not those
//     carried-on segments;
//   - the pending store holds what stopped to ask the user;
//   - the task hub holds background jobs, and is the only thing that can cancel them.
//
// The database's "running" task rows are deliberately not used: a process that
// died leaves them behind, and listing them would offer to stop what is not running.

// RunningWork is the answer to GetRunningWork.
type RunningWork struct {
	Items   []RunningWorkItem `json:"items"`
	Running int               `json:"running"` // conversations, runs and tasks at work (queued included)
	Waiting int               `json:"waiting"` // stopped until the user decides
	At      int64             `json:"at"`      // unix seconds
}

// RunningWorkItem is one row of it. The page composes the wording from these
// fields; nothing here is meant to be shown as is.
type RunningWorkItem struct {
	Kind         string `json:"kind"`                    // session | run | task | waiting
	ID           string `json:"id"`                      // unique within the list
	SessionID    string `json:"session_id,omitempty"`    // the conversation, when there is one
	Known        bool   `json:"known"`                   // that conversation exists, so it can be opened
	Title        string `json:"title,omitempty"`         // session: its title; task: what it was asked to do
	SessionTitle string `json:"session_title,omitempty"` // task: the title of the conversation it works in
	State        string `json:"state"`                   // running | queued | awaiting_approval | awaiting_answer
	Since        int64  `json:"since,omitempty"`         // unix seconds; 0 when unknown
	Tool         string `json:"tool,omitempty"`          // waiting: what it wants to run
	Count        int    `json:"count,omitempty"`         // waiting: requests stopped in this conversation
	Source       string `json:"source,omitempty"`        // waiting without a conversation: workflow | matrix
	TaskID       string `json:"task_id,omitempty"`       // task: what to cancel
	TaskKind     string `json:"task_kind,omitempty"`     // task: cron | trigger | swarm | manual | matrix_followup
	RunID        string `json:"run_id,omitempty"`        // run without a conversation: what to cancel
	Engine       string `json:"engine,omitempty"`        // run without a conversation
	Stoppable    bool   `json:"stoppable"`
}

const (
	workRunning          = "running"
	workQueued           = "queued"
	workAwaitingApproval = "awaiting_approval"
	workAwaitingAnswer   = "awaiting_answer"
)

// runningWorkInputs is what the sources said, taken at (nearly) the same moment.
type runningWorkInputs struct {
	runner   []agent.RunningSession
	resuming []resumingRun
	engines  []engine.ActiveRun
	pending  []core.PendingRun
	tasks    []taskhub.Task
	titles   map[string]string // conversation id -> title, for the ids that exist
	now      time.Time
}

func (in runningWorkInputs) sessionIDs() []string {
	var ids []string
	for _, r := range in.runner {
		ids = append(ids, r.SessionID)
	}
	for _, r := range in.resuming {
		ids = append(ids, r.SessionID)
	}
	for _, r := range in.engines {
		ids = append(ids, r.SessionID)
	}
	for _, p := range in.pending {
		ids = append(ids, p.SessionID)
	}
	for _, t := range in.tasks {
		ids = append(ids, t.SessionID)
	}
	return ids
}

// GetRunningWork lists everything at work or waiting for the user in this
// process. Read-only and cheap enough to poll.
func (s *AppService) GetRunningWork() RunningWork {
	in := runningWorkInputs{now: time.Now()}
	// A conversation moves between "stopped for the user", "being resumed" and "at
	// work", and the sources are read one after another. Read what waits first and
	// again last, in between the sources it moves to, so that a move in either
	// direction is caught by one of the reads instead of falling between them.
	var waitingFirst []core.PendingRun
	if s.rt != nil {
		waitingFirst = s.rt.pending.Snapshot()
		in.resuming = s.rt.resuming.snapshot()
	}
	if s.rt != nil && s.rt.engineRouter != nil {
		in.engines = s.rt.engineRouter.Active()
	}
	if s.rt != nil {
		if runner := s.rt.AgentRunner(); runner != nil {
			in.runner = runner.RunningSessions()
		}
		if s.rt.taskHub != nil {
			in.tasks = s.rt.taskHub.Active()
		}
		in.pending = mergePending(waitingFirst, s.rt.pending.Snapshot())
		if store := s.rt.Sessions(); store != nil {
			// By id, not from the recent list: what is running may be an old conversation.
			if titles, err := store.Titles(context.Background(), in.sessionIDs()); err == nil {
				in.titles = titles
			}
		}
	}
	return buildRunningWork(in)
}

// CancelBackgroundTask stops a background task this process is running.
func (s *AppService) CancelBackgroundTask(taskID string) map[string]any {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return map[string]any{"success": false, "error": "empty task_id"}
	}
	if s.rt == nil || s.rt.taskHub == nil {
		return map[string]any{"success": false, "error": "task hub unavailable"}
	}
	if !s.rt.taskHub.Cancel(taskID) {
		return map[string]any{"success": false, "error": "这个任务没有在本程序里运行，可能已经结束了。"}
	}
	return map[string]any{"success": true}
}

// mergePending is the union of two reads of the pending store, by approval id;
// where both have an entry the later read wins.
func mergePending(earlier, later []core.PendingRun) []core.PendingRun {
	if len(earlier) == 0 {
		return later
	}
	seen := make(map[string]bool, len(later))
	out := make([]core.PendingRun, 0, len(later)+len(earlier))
	for _, p := range later {
		seen[p.ApprovalID] = true
		out = append(out, p)
	}
	for _, p := range earlier {
		if !seen[p.ApprovalID] {
			out = append(out, p)
		}
	}
	return out
}

// resumingRun is a paused conversation turn that the user has just decided to
// carry on. It is at work from the moment the right to resume is claimed, but
// the runner only knows it once the resumed run has registered its cancel
// handle, a few database writes later. By then it has left the pending store, so
// without this note it would drop out of the list for a moment after "approve".
type resumingRun struct {
	SessionID string
	Since     time.Time
}

// resumeTracker keeps those notes. The zero value is ready to use.
type resumeTracker struct {
	mu   sync.Mutex
	next uint64
	runs map[uint64]resumingRun
}

// begin notes that a turn of the conversation is being resumed; call the
// returned function when the resume is over. A blank conversation id is ignored.
func (t *resumeTracker) begin(sessionID string) (end func()) {
	sessionID = strings.TrimSpace(sessionID)
	if t == nil || sessionID == "" {
		return func() {}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.runs == nil {
		t.runs = map[uint64]resumingRun{}
	}
	t.next++
	id := t.next
	t.runs[id] = resumingRun{SessionID: sessionID, Since: time.Now()}
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		delete(t.runs, id)
	}
}

func (t *resumeTracker) snapshot() []resumingRun {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]resumingRun, 0, len(t.runs))
	for _, r := range t.runs {
		out = append(out, r)
	}
	return out
}

func buildRunningWork(in runningWorkInputs) RunningWork {
	var items []*RunningWorkItem
	atWork := map[string]bool{}                 // conversations at work, however they are shown
	sessionRow := map[string]*RunningWorkItem{} // the row a conversation at work is shown in

	// Conversations at work. The runner and the router overlap, and a carried-on
	// segment is only in the runner, so this is a union keyed by conversation.
	work := func(sid string, since time.Time) {
		it := sessionRow[sid]
		if it == nil {
			it = &RunningWorkItem{Kind: "session", ID: "session:" + sid, SessionID: sid, State: workRunning, Stoppable: true}
			sessionRow[sid] = it
			atWork[sid] = true
			items = append(items, it)
		}
		if ts := unixOf(since); ts > 0 && (it.Since == 0 || ts < it.Since) {
			it.Since = ts
		}
	}
	for _, r := range in.runner {
		if sid := strings.TrimSpace(r.SessionID); sid != "" {
			work(sid, r.StartedAt)
		}
	}
	for _, r := range in.resuming {
		if sid := strings.TrimSpace(r.SessionID); sid != "" {
			work(sid, r.Since)
		}
	}
	for _, r := range in.engines {
		if sid := strings.TrimSpace(r.SessionID); sid != "" {
			work(sid, r.StartedAt)
			continue
		}
		items = append(items, &RunningWorkItem{
			Kind: "run", ID: "run:" + r.RunID, RunID: r.RunID, Engine: r.Engine,
			State: workRunning, Since: unixOf(r.StartedAt), Stoppable: true,
		})
	}

	// Background tasks. A task that works in a conversation makes that conversation
	// show up as at work too; one piece of work gets one row, the task's, because
	// stopping the task is what stops it.
	absorbed := map[*RunningWorkItem]bool{}
	for _, t := range in.tasks {
		sid := strings.TrimSpace(t.SessionID)
		it := &RunningWorkItem{
			Kind: "task", ID: "task:" + t.ID, TaskID: t.ID, TaskKind: t.Kind, SessionID: sid,
			Title: taskSummary(t), State: workRunning, Since: t.CreatedAt, Stoppable: true,
		}
		if t.Status == taskhub.StatusQueued {
			it.State = workQueued
		}
		if row := sessionRow[sid]; sid != "" && row != nil {
			absorbed[row] = true
		}
		items = append(items, it)
	}

	// What stopped to ask the user. A conversation that is at work again no longer
	// waits, whatever the store still says.
	type stopped struct {
		first core.PendingRun // the oldest request
		n     int
	}
	waiting := map[string]*stopped{}
	var waitKeys []string
	for _, p := range in.pending {
		key := strings.TrimSpace(p.SessionID)
		if key == "" {
			key = "approval:" + p.ApprovalID
		}
		w := waiting[key]
		if w == nil {
			waiting[key] = &stopped{first: p, n: 1}
			waitKeys = append(waitKeys, key)
			continue
		}
		w.n++
		if pendingBefore(p, w.first) {
			w.first = p
		}
	}
	for _, key := range waitKeys {
		w := waiting[key]
		sid := strings.TrimSpace(w.first.SessionID)
		if sid != "" && atWork[sid] {
			continue
		}
		it := &RunningWorkItem{
			Kind: "session", ID: "session:" + sid, SessionID: sid, State: workAwaitingApproval,
			Since: w.first.CreatedAt, Tool: w.first.ToolName, Count: w.n,
		}
		if w.first.ToolName == "ask_user" {
			it.State = workAwaitingAnswer
		}
		if sid == "" {
			it.Kind, it.ID, it.Source = "waiting", key, w.first.ResumeKind
		}
		items = append(items, it)
	}

	for _, it := range items {
		if it.SessionID == "" {
			continue
		}
		title, known := in.titles[it.SessionID]
		it.Known = known
		if it.Kind == "task" {
			it.SessionTitle = title
		} else {
			it.Title = title
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if ra, rb := stateRank(a.State), stateRank(b.State); ra != rb {
			return ra < rb
		}
		if sa, sb := sinceKey(a.Since), sinceKey(b.Since); sa != sb {
			return sa < sb
		}
		return a.ID < b.ID
	})

	out := RunningWork{Items: make([]RunningWorkItem, 0, len(items)), At: in.now.Unix()}
	for _, it := range items {
		if absorbed[it] {
			continue
		}
		if stateRank(it.State) == 0 {
			out.Waiting++
		} else {
			out.Running++
		}
		out.Items = append(out.Items, *it)
	}
	return out
}

// stateRank orders the list: what needs the user first, then what is at work,
// then what has not started.
func stateRank(state string) int {
	switch state {
	case workAwaitingApproval, workAwaitingAnswer:
		return 0
	case workRunning:
		return 1
	}
	return 2
}

// sinceKey sorts an unknown start time after every known one.
func sinceKey(since int64) int64 {
	if since <= 0 {
		return math.MaxInt64
	}
	return since
}

// pendingBefore says whether a stopped earlier than b did; a row saved before the
// time was recorded counts as the latest.
func pendingBefore(a, b core.PendingRun) bool {
	if ka, kb := sinceKey(a.CreatedAt), sinceKey(b.CreatedAt); ka != kb {
		return ka < kb
	}
	return a.ApprovalID < b.ApprovalID
}

func unixOf(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// taskSummary is what a task was asked to do, short enough for one line.
func taskSummary(t taskhub.Task) string {
	text := t.Input
	if t.Kind == "swarm" {
		var p struct {
			Topic string `json:"topic"`
		}
		if json.Unmarshal([]byte(t.Input), &p) == nil && strings.TrimSpace(p.Topic) != "" {
			text = p.Topic
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return clipRunes(line, 60)
		}
	}
	return ""
}

func clipRunes(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}
