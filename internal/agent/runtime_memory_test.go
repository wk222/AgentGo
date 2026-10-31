package agent

import (
	"agentgo/internal/memory"
	"agentgo/internal/sessions"
	"context"
	"errors"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/plantask"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"strings"
	"testing"
)

func TestOfficialPlanTaskWritesFeedFreshScopedProjection(t *testing.T) {
	root := t.TempDir()
	ctx := WithSessionID(context.Background(), "chat-a")
	dir := planTaskDirectory(ctx, root, root)
	if dir == planTaskDirectory(WithSessionID(ctx, "chat-b"), root, root) {
		t.Fatal("tasks shared across sessions")
	}
	if dir != planTaskDirectory(WithSessionID(ctx, "agentic:chat-a"), root, root) {
		t.Fatal("transport prefix changed namespace")
	}
	mw, err := plantask.New(ctx, &plantask.Config{Backend: newDiskPlantaskBackend(dir), BaseDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := mw.BeforeAgent(ctx, &adk.ChatModelAgentContext{})
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(name, input string) {
		t.Helper()
		for _, candidate := range run.Tools {
			info, err := candidate.Info(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if info.Name == name {
				invokable, ok := candidate.(tool.InvokableTool)
				if !ok {
					t.Fatal("not invokable")
				}
				if _, err := invokable.InvokableRun(ctx, input); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
		t.Fatal("missing official tool", name)
	}
	invoke("TaskCreate", `{"subject":"compile after tests","description":"keep existing tests"}`)
	tasks, err := loadPlanTaskSnapshot(ctx, dir)
	if err != nil || len(tasks) != 1 || tasks[0].Status != "pending" {
		t.Fatal(tasks, err)
	}
	invoke("TaskCreate", `{"subject":"review final results","description":"check output before delivery"}`)
	invoke("TaskUpdate", `{"taskId":"1","status":"completed"}`)
	tasks, err = loadPlanTaskSnapshot(ctx, dir)
	completed := false
	for _, task := range tasks {
		if task.ID == "1" && task.Status == "completed" {
			completed = true
		}
	}
	if err != nil || len(tasks) != 2 || !completed {
		t.Fatal(tasks, err)
	}
	view := sessions.CompileProjectedView([]*schema.Message{schema.UserMessage("continue")}, sessions.CompileOptions{PlanTasks: tasks})
	if !strings.Contains(strings.Join(view.TaskState, "\n"), "verification requires tool evidence") {
		t.Fatal("planning status promoted to verified success")
	}
	// Official PlanTask clears the task list when every task is completed.
	invoke("TaskUpdate", `{"taskId":"2","status":"completed"}`)
	cleared, err := loadPlanTaskSnapshot(ctx, dir)
	if err != nil || len(cleared) != 0 {
		t.Fatal("official completion cleanup not reflected", cleared, err)
	}
	other, err := loadPlanTaskSnapshot(ctx, planTaskDirectory(WithSessionID(ctx, "chat-b"), root, root))
	if err != nil || len(other) != 0 {
		t.Fatal("foreign tasks exposed", other, err)
	}
}

func TestPersistentMemoryRevisionInvalidatesLiveRecallCache(t *testing.T) {
	store, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB().Close()
	ctx := context.Background()
	p := memory.NewPipeline(store)
	m := NewMemoryMiddleware(p)
	if err := p.Ingest(ctx, memory.Record{ID: "old", Scope: "s", Content: "compiler OLD-CHOICE", Modality: "fact", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if got := m.cachedRecallForQuery(ctx, "s", "compiler"); !strings.Contains(got, "OLD-CHOICE") {
		t.Fatal(got)
	}
	if err := p.Ingest(ctx, memory.Record{ID: "new", Scope: "s", Content: "compiler NEW-CHOICE", Modality: "fact", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SupersedeRecords(ctx, "s", "new", "old"); err != nil {
		t.Fatal(err)
	}
	got := m.cachedRecallForQuery(ctx, "s", "compiler")
	if strings.Contains(got, "OLD-CHOICE") || !strings.Contains(got, "NEW-CHOICE") {
		t.Fatal("stale TTL cache survived correction", got)
	}
}

func TestOfficialSummaryFinalizerRejectsChangedMemorySources(t *testing.T) {
	version := "initial"
	ctx := sessions.WithMemoryRevision(context.Background(), version, func(context.Context) (string, error) { return version, nil })
	cfg := contextSummaryConfig[*schema.Message](nil, CanvasPolicyFor(CanvasBalanced))
	version = "corrected"
	_, err := cfg.Finalize(ctx, []*schema.Message{schema.UserMessage(strings.Repeat("source ", 1000))}, schema.AssistantMessage("outdated summary", nil))
	if !errors.Is(err, sessions.ErrSummarySourceChanged) {
		t.Fatal("stale official summary accepted", err)
	}
}

func TestSpineLeavesSmallHistoryForOfficialSummarization(t *testing.T) {
	ctx := WithSessionID(context.Background(), "s")
	var msgs []*schema.Message
	for i := 0; i < 12; i++ {
		msgs = append(msgs, schema.UserMessage("REQUEST-"+strings.Repeat("x", i+1)), schema.AssistantMessage("ack", nil))
	}
	mw := NewSessionSpineMiddleware(SessionSpineConfig{MaxActiveTurns: 4, DataDir: t.TempDir()})
	_, out, err := mw.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{Messages: msgs}, &adk.ModelContext{})
	if err != nil {
		t.Fatal(err)
	}
	users := 0
	for _, m := range out.Messages {
		if m.Role == schema.User && !sessions.IsContextMessage(m) {
			users++
		}
	}
	if users != 12 {
		t.Fatal("history pruned before official token-based summarization", users)
	}
}
