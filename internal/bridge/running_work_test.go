package bridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"agentgo/internal/agent"
	"agentgo/internal/core"
	"agentgo/internal/engine"
	"agentgo/internal/taskhub"
)

var workT0 = time.Unix(1_700_000_000, 0)

func workAt(sec int) time.Time { return workT0.Add(time.Duration(sec) * time.Second) }
func workUnix(sec int) int64   { return workAt(sec).Unix() }

func workIDs(w RunningWork) []string {
	ids := make([]string, 0, len(w.Items))
	for _, it := range w.Items {
		ids = append(ids, it.ID)
	}
	return ids
}

func workItem(t *testing.T, w RunningWork, id string) RunningWorkItem {
	t.Helper()
	for _, it := range w.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no %q in %v", id, workIDs(w))
	return RunningWorkItem{}
}

func sameIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestRunningWorkListsEveryConversationAtWorkOnce(t *testing.T) {
	w := buildRunningWork(runningWorkInputs{
		now: workAt(100),
		// The runner and the router overlap on B; C is only known to the router.
		runner: []agent.RunningSession{{SessionID: "A", StartedAt: workAt(10)}, {SessionID: "B", StartedAt: workAt(20)}},
		engines: []engine.ActiveRun{
			{RunID: "r1", Engine: "agentic", SessionID: "B", StartedAt: workAt(15)},
			{RunID: "r2", Engine: "agentic", SessionID: "C", StartedAt: workAt(30)},
		},
		titles: map[string]string{"A": "写周报", "B": "重构方案"},
	})
	if want := []string{"session:A", "session:B", "session:C"}; !sameIDs(workIDs(w), want) {
		t.Fatalf("items = %v, want %v", workIDs(w), want)
	}
	if w.Running != 3 || w.Waiting != 0 || w.At != workUnix(100) {
		t.Fatalf("counts = running %d, waiting %d, at %d", w.Running, w.Waiting, w.At)
	}
	a, b, c := workItem(t, w, "session:A"), workItem(t, w, "session:B"), workItem(t, w, "session:C")
	if a.Title != "写周报" || !a.Known || a.State != "running" || !a.Stoppable || a.Since != workUnix(10) {
		t.Fatalf("A = %+v", a)
	}
	if b.Since != workUnix(15) {
		t.Fatalf("B started when the earlier of the two sources says: %+v", b)
	}
	if c.Known || c.Title != "" || c.SessionID != "C" {
		t.Fatalf("C has no row in the list and must not claim to: %+v", c)
	}
}

// After an approval or an answer the conversation carries on outside the router;
// only the runner's cancel handle knows it is running.
func TestRunningWorkKeepsACarriedOnSegmentTheRouterMissed(t *testing.T) {
	w := buildRunningWork(runningWorkInputs{
		now:    workAt(50),
		runner: []agent.RunningSession{{SessionID: "A", StartedAt: workAt(40)}},
		titles: map[string]string{"A": "写周报"},
	})
	if want := []string{"session:A"}; !sameIDs(workIDs(w), want) || w.Running != 1 {
		t.Fatalf("items = %v, running %d", workIDs(w), w.Running)
	}
}

func TestRunningWorkEngineRunWithoutAConversation(t *testing.T) {
	w := buildRunningWork(runningWorkInputs{
		now:     workAt(50),
		engines: []engine.ActiveRun{{RunID: "r9", Engine: "voice", StartedAt: workAt(45)}},
	})
	it := workItem(t, w, "run:r9")
	if it.Kind != "run" || it.RunID != "r9" || it.Engine != "voice" || it.SessionID != "" || it.Known || !it.Stoppable || it.Since != workUnix(45) {
		t.Fatalf("run = %+v", it)
	}
}

func TestRunningWorkWaitingGroupsByConversationAndNamesTheOldestRequest(t *testing.T) {
	w := buildRunningWork(runningWorkInputs{
		now: workAt(500),
		pending: []core.PendingRun{
			{SessionID: "A", ToolName: "execute_bash", ApprovalID: "ap2", CreatedAt: workUnix(200)},
			{SessionID: "A", ToolName: "ask_user", ApprovalID: "ap1", CreatedAt: workUnix(100)},
			{SessionID: "B", ToolName: "write_file", ApprovalID: "ap3", CreatedAt: workUnix(150)},
		},
		titles: map[string]string{"A": "写周报", "B": "重构方案"},
	})
	if want := []string{"session:A", "session:B"}; !sameIDs(workIDs(w), want) {
		t.Fatalf("items = %v, want %v", workIDs(w), want)
	}
	if w.Waiting != 2 || w.Running != 0 {
		t.Fatalf("counts = running %d, waiting %d", w.Running, w.Waiting)
	}
	a, b := workItem(t, w, "session:A"), workItem(t, w, "session:B")
	if a.State != "awaiting_answer" || a.Tool != "ask_user" || a.Count != 2 || a.Since != workUnix(100) || a.Title != "写周报" || a.Stoppable {
		t.Fatalf("A = %+v", a)
	}
	if b.State != "awaiting_approval" || b.Tool != "write_file" || b.Count != 1 || b.Since != workUnix(150) || !b.Known {
		t.Fatalf("B = %+v", b)
	}
}

func TestRunningWorkAConversationAtWorkAgainIsNotWaiting(t *testing.T) {
	w := buildRunningWork(runningWorkInputs{
		now:     workAt(60),
		runner:  []agent.RunningSession{{SessionID: "A", StartedAt: workAt(55)}},
		pending: []core.PendingRun{{SessionID: "A", ToolName: "execute_bash", ApprovalID: "ap1", CreatedAt: workUnix(50)}},
	})
	if len(w.Items) != 1 || w.Items[0].State != "running" || w.Running != 1 || w.Waiting != 0 {
		t.Fatalf("items = %+v, running %d, waiting %d", w.Items, w.Running, w.Waiting)
	}
}

func TestRunningWorkWaitingWithoutAConversation(t *testing.T) {
	w := buildRunningWork(runningWorkInputs{
		now:     workAt(60),
		pending: []core.PendingRun{{ApprovalID: "ap9", ResumeKind: "workflow", WorkflowID: "wf", ToolName: "http_request"}},
	})
	it := workItem(t, w, "approval:ap9")
	if it.Kind != "waiting" || it.Source != "workflow" || it.Tool != "http_request" || it.Known || it.SessionID != "" || it.Since != 0 {
		t.Fatalf("waiting = %+v", it)
	}
	if w.Waiting != 1 {
		t.Fatalf("waiting = %d", w.Waiting)
	}
}

func TestRunningWorkATaskAndItsConversationAreOneRow(t *testing.T) {
	w := buildRunningWork(runningWorkInputs{
		now: workAt(100),
		// The task's run registers its conversation with the runner, so A is "at work" too.
		runner: []agent.RunningSession{{SessionID: "A", StartedAt: workAt(5)}, {SessionID: "B", StartedAt: workAt(6)}},
		tasks: []taskhub.Task{
			{ID: "t1", Kind: "cron", SessionID: "A", Input: "\n 每日巡检\n第二行", Status: taskhub.StatusRunning, CreatedAt: workUnix(4)},
			{ID: "t2", Kind: "swarm", Input: `{"topic":"评审方案","agent_ids_json":"[]"}`, Status: taskhub.StatusQueued, CreatedAt: workUnix(8)},
		},
		titles: map[string]string{"A": "周报", "B": "B 会话"},
	})
	if want := []string{"task:t1", "session:B", "task:t2"}; !sameIDs(workIDs(w), want) {
		t.Fatalf("items = %v, want %v", workIDs(w), want)
	}
	if w.Running != 3 {
		t.Fatalf("running = %d", w.Running)
	}
	t1, t2 := workItem(t, w, "task:t1"), workItem(t, w, "task:t2")
	if t1.TaskID != "t1" || t1.TaskKind != "cron" || t1.Title != "每日巡检" || t1.SessionID != "A" || t1.SessionTitle != "周报" || !t1.Known || t1.State != "running" || !t1.Stoppable {
		t.Fatalf("t1 = %+v", t1)
	}
	if t2.Title != "评审方案" || t2.State != "queued" || t2.SessionID != "" || t2.Known || !t2.Stoppable {
		t.Fatalf("t2 = %+v", t2)
	}
}

func TestRunningWorkAsksForTheUserFirstAndQueuedLast(t *testing.T) {
	w := buildRunningWork(runningWorkInputs{
		now:     workAt(1000),
		runner:  []agent.RunningSession{{SessionID: "run-old", StartedAt: workAt(1)}},
		pending: []core.PendingRun{{SessionID: "ask-new", ToolName: "execute_bash", ApprovalID: "ap", CreatedAt: workUnix(900)}},
		tasks:   []taskhub.Task{{ID: "q", Kind: "manual", Status: taskhub.StatusQueued, CreatedAt: workUnix(0)}},
	})
	if want := []string{"session:ask-new", "session:run-old", "task:q"}; !sameIDs(workIDs(w), want) {
		t.Fatalf("items = %v, want %v", workIDs(w), want)
	}
	if w.Running != 2 || w.Waiting != 1 {
		t.Fatalf("counts = running %d, waiting %d", w.Running, w.Waiting)
	}
}

func TestRunningWorkNothingIsAnEmptyListNotNull(t *testing.T) {
	w := buildRunningWork(runningWorkInputs{now: workAt(1)})
	if w.Items == nil || len(w.Items) != 0 || w.Running != 0 || w.Waiting != 0 {
		t.Fatalf("empty = %+v", w)
	}
	b, err := json.Marshal(w)
	if err != nil || !strings.Contains(string(b), `"items":[]`) {
		t.Fatalf("json = %s, %v", b, err)
	}
}

func TestTaskSummaryIsOneShortLine(t *testing.T) {
	long := strings.Repeat("长", 80)
	cases := []struct {
		name string
		task taskhub.Task
		want string
	}{
		{"first non-blank line", taskhub.Task{Kind: "manual", Input: "\n\n  整理本周日志  \n其余"}, "整理本周日志"},
		{"clipped by characters, not bytes", taskhub.Task{Kind: "manual", Input: long}, strings.Repeat("长", 60) + "…"},
		{"swarm shows its topic", taskhub.Task{Kind: "swarm", Input: `{"topic":"方案评审"}`}, "方案评审"},
		{"swarm without a topic falls back to the input", taskhub.Task{Kind: "swarm", Input: "not json"}, "not json"},
		{"nothing to say", taskhub.Task{Kind: "manual"}, ""},
	}
	for _, c := range cases {
		if got := taskSummary(c.task); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// GetRunningWork reads every source for real: the router, the pending store, the
// task hub, and the titles in the session store (of conversations nobody listed).
func TestGetRunningWorkReadsEverySourceAndCancelStopsATask(t *testing.T) {
	svc, store, _ := sessionService(t)
	ctx := context.Background()
	running, err := store.Create(ctx, "正在跑的")
	if err != nil {
		t.Fatal(err)
	}
	asking, err := store.Create(ctx, "停在批准上的")
	if err != nil {
		t.Fatal(err)
	}

	eng := blockingEngine{started: make(chan struct{}), release: make(chan struct{})}
	svc.rt.engineRouter = engine.NewRouter()
	svc.rt.engineRouter.Register(eng)
	engineDone := make(chan struct{})
	go func() {
		defer close(engineDone)
		svc.rt.engineRouter.Run(ctx, engine.RunRequest{Engine: "block", SessionID: running.ID, Input: "go"}, nil)
	}()
	select {
	case <-eng.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the run never started")
	}
	defer func() {
		close(eng.release)
		<-engineDone
	}()

	svc.rt.pending = core.NewPendingStore()
	svc.rt.pending.Set(core.PendingRun{SessionID: asking.ID, ToolName: "execute_bash", ApprovalID: "ap-1"})

	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = conn.Close() })
	hub, err := taskhub.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	taskStarted := make(chan struct{})
	hub.SetRunner(func(ctx context.Context, _ taskhub.Task, _ func(taskhub.Event)) error {
		close(taskStarted)
		<-ctx.Done()
		return ctx.Err()
	})
	svc.rt.taskHub = hub
	task, err := hub.Start("manual", "", "整理日志")
	if err != nil {
		t.Fatal(err)
	}
	<-taskStarted

	w := svc.GetRunningWork()
	if w.Running != 2 || w.Waiting != 1 {
		t.Fatalf("counts = running %d, waiting %d, items %+v", w.Running, w.Waiting, w.Items)
	}
	run := workItem(t, w, "session:"+running.ID)
	if run.State != "running" || run.Title != "正在跑的" || !run.Known || !run.Stoppable {
		t.Fatalf("running session = %+v", run)
	}
	ask := workItem(t, w, "session:"+asking.ID)
	if ask.State != "awaiting_approval" || ask.Title != "停在批准上的" || ask.Tool != "execute_bash" || ask.Since == 0 {
		t.Fatalf("awaiting session = %+v", ask)
	}
	tk := workItem(t, w, "task:"+task.ID)
	if tk.TaskKind != "manual" || tk.Title != "整理日志" || !tk.Stoppable {
		t.Fatalf("task = %+v", tk)
	}
	if ids := workIDs(w); ids[0] != "session:"+asking.ID {
		t.Fatalf("what waits for the user must come first: %v", ids)
	}

	if got := svc.CancelBackgroundTask(" " + task.ID + " "); got["success"] != true {
		t.Fatalf("cancel: %v", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(hub.Active()) != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if after := svc.GetRunningWork(); after.Running != 1 || after.Waiting != 1 {
		t.Fatalf("after cancelling the task: running %d, waiting %d", after.Running, after.Waiting)
	}
	for _, id := range []string{task.ID, "no-such-task"} {
		if got := svc.CancelBackgroundTask(id); got["success"] != false || got["error"] == "" {
			t.Fatalf("cancelling %q again: %v", id, got)
		}
	}
	if got := svc.CancelBackgroundTask("  "); got["success"] != false {
		t.Fatalf("empty id: %v", got)
	}
}

// The user approves; the pending run is claimed at once, but the resumed run only
// shows up in the runner a few writes later. In between the conversation must not
// vanish, and it must not be listed twice once the runner knows it.
func TestRunningWorkAResumedRunIsListedBeforeTheRunnerKnowsIt(t *testing.T) {
	w := buildRunningWork(runningWorkInputs{
		now:      workAt(50),
		resuming: []resumingRun{{SessionID: "A", Since: workAt(40)}},
		titles:   map[string]string{"A": "写周报"},
	})
	if want := []string{"session:A"}; !sameIDs(workIDs(w), want) || w.Running != 1 || w.Waiting != 0 {
		t.Fatalf("items = %v, running %d, waiting %d", workIDs(w), w.Running, w.Waiting)
	}
	if a := workItem(t, w, "session:A"); a.State != "running" || a.Since != workUnix(40) || a.Title != "写周报" || !a.Known || !a.Stoppable {
		t.Fatalf("A = %+v", a)
	}

	// Once the runner has it too, and a stale pending entry is still around,
	// there is still one row, started when the earlier note says.
	w = buildRunningWork(runningWorkInputs{
		now:      workAt(50),
		runner:   []agent.RunningSession{{SessionID: "A", StartedAt: workAt(42)}},
		resuming: []resumingRun{{SessionID: "A", Since: workAt(40)}, {SessionID: " ", Since: workAt(1)}},
		pending:  []core.PendingRun{{SessionID: "A", ToolName: "execute_bash", ApprovalID: "ap1", CreatedAt: workUnix(30)}},
	})
	if len(w.Items) != 1 || w.Running != 1 || w.Waiting != 0 || w.Items[0].State != "running" || w.Items[0].Since != workUnix(40) {
		t.Fatalf("items = %+v, running %d, waiting %d", w.Items, w.Running, w.Waiting)
	}
}

func TestResumeTrackerNotesAndForgets(t *testing.T) {
	var tr resumeTracker
	if got := tr.snapshot(); len(got) != 0 {
		t.Fatalf("new tracker: %+v", got)
	}
	endA1 := tr.begin("A")
	endA2 := tr.begin(" A ")
	endNone := tr.begin("   ") // no conversation: nothing to list
	if got := tr.snapshot(); len(got) != 2 || got[0].SessionID != "A" || got[1].SessionID != "A" || got[0].Since.IsZero() {
		t.Fatalf("two resumes of A: %+v", got)
	}
	endNone()
	endA1()
	endA1() // ending twice must not take the other note with it
	if got := tr.snapshot(); len(got) != 1 {
		t.Fatalf("after one ended: %+v", got)
	}
	endA2()
	if got := tr.snapshot(); len(got) != 0 {
		t.Fatalf("after both ended: %+v", got)
	}

	var none *resumeTracker
	none.begin("A")() // a missing tracker is harmless
	if got := none.snapshot(); got != nil {
		t.Fatalf("nil tracker: %+v", got)
	}
}

// End to end through resolveApproval: while the resume is in flight the claimed
// conversation is listed as running (not waiting, not missing), and after it ends
// it is gone. The scripted resume does not register with a runner, as the real one
// does a moment later, so only the note can make it show up.
func TestGetRunningWorkKeepsAConversationThatIsBeingResumed(t *testing.T) {
	g := newResumeRig(t)
	id := g.pause(t, "execute_bash")
	g.startRun(t, "run_w")

	if w := g.s.GetRunningWork(); w.Waiting != 1 || w.Running != 0 {
		t.Fatalf("paused: running %d, waiting %d, %+v", w.Running, w.Waiting, w.Items)
	}

	inResume := make(chan struct{})
	release := make(chan struct{})
	g.script = func(context.Context, int, core.PendingRun) (*agent.RunResult, error) {
		close(inResume)
		<-release
		return &agent.RunResult{Content: "done", UsedTools: true}, nil
	}
	done := make(chan map[string]any, 1)
	go func() { done <- resolve(g, context.Background(), id, true) }()
	select {
	case <-inResume:
	case <-time.After(5 * time.Second):
		t.Fatal("the resume never started")
	}

	w := g.s.GetRunningWork()
	if w.Running != 1 || w.Waiting != 0 || len(w.Items) != 1 {
		t.Fatalf("while resuming: running %d, waiting %d, %+v", w.Running, w.Waiting, w.Items)
	}
	if it := w.Items[0]; it.SessionID != g.sid || it.State != "running" || it.Kind != "session" || it.Since == 0 {
		t.Fatalf("while resuming: %+v", it)
	}

	close(release)
	select {
	case out := <-done:
		if out["success"] != true || out["resume_error"] != nil {
			t.Fatalf("resolve: %+v", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the resume never finished")
	}
	if w := g.s.GetRunningWork(); len(w.Items) != 0 {
		t.Fatalf("after the resume: %+v", w.Items)
	}
}

// However a decision ends, it must not leave a conversation "at work" behind:
// a stuck note would offer to stop what is not running, for good.
func TestResolveApprovalNeverLeavesAResumeNoteBehind(t *testing.T) {
	g := newResumeRig(t)
	g.startRun(t, "run_n")
	quiet := func(what string) {
		t.Helper()
		if got := g.s.rt.resuming.snapshot(); len(got) != 0 {
			t.Fatalf("%s left a note: %+v", what, got)
		}
	}

	failing := g.pause(t, "execute_bash")
	g.script = func(context.Context, int, core.PendingRun) (*agent.RunResult, error) {
		return nil, errors.New("模型不可用")
	}
	if out := resolve(g, context.Background(), failing, true); out["resume_error"] == nil {
		t.Fatalf("a failing resume must say so: %+v", out)
	}
	quiet("a failed resume")

	if out := resolve(g, context.Background(), "no-such-approval", true); out["success"] == true && out["resume_error"] == nil {
		t.Fatalf("approving what does not exist must not look like success: %+v", out)
	}
	quiet("approving an unknown request")

	refused := g.pause(t, "execute_bash")
	_ = resolve(g, context.Background(), refused, false)
	quiet("a refusal")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := g.pause(t, "execute_bash")
	_ = resolve(g, ctx, cancelled, true)
	quiet("a decision on a cancelled context")

	if w := g.s.GetRunningWork(); w.Running != 0 {
		t.Fatalf("nothing is at work: %+v", w.Items)
	}
}

func TestMergePendingKeepsEitherReadAndPrefersTheLater(t *testing.T) {
	a1 := core.PendingRun{ApprovalID: "a", ToolName: "old", SessionID: "S"}
	a2 := core.PendingRun{ApprovalID: "a", ToolName: "new", SessionID: "S"}
	b := core.PendingRun{ApprovalID: "b", SessionID: "T"}
	c := core.PendingRun{ApprovalID: "c", SessionID: "U"}

	if got := mergePending(nil, []core.PendingRun{c}); len(got) != 1 || got[0].ApprovalID != "c" {
		t.Fatalf("nothing earlier: %+v", got)
	}
	if got := mergePending([]core.PendingRun{b}, nil); len(got) != 1 || got[0].ApprovalID != "b" {
		t.Fatalf("nothing later keeps what was read first: %+v", got)
	}
	got := mergePending([]core.PendingRun{a1, b}, []core.PendingRun{a2, c})
	ids := map[string]string{}
	for _, p := range got {
		ids[p.ApprovalID] = p.ToolName
	}
	if len(got) != 3 || len(ids) != 3 || ids["a"] != "new" {
		t.Fatalf("merged = %+v", got)
	}
	if _, ok := ids["b"]; !ok {
		t.Fatalf("what only the earlier read saw must stay: %+v", got)
	}
	if _, ok := ids["c"]; !ok {
		t.Fatalf("what only the later read saw must stay: %+v", got)
	}
}

func TestGetRunningWorkWithoutAnySourceIsEmpty(t *testing.T) {
	svc := &AppService{rt: &Runtime{}}
	if w := svc.GetRunningWork(); w.Items == nil || len(w.Items) != 0 {
		t.Fatalf("bare service: %+v", w)
	}
	if got := svc.CancelBackgroundTask("x"); got["success"] != false {
		t.Fatalf("without a hub: %v", got)
	}
	if w := (&AppService{}).GetRunningWork(); len(w.Items) != 0 {
		t.Fatalf("no runtime: %+v", w)
	}
}
