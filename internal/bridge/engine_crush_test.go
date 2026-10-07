package bridge

import (
	"context"
	"strings"
	"testing"

	"agentgo/internal/engine"
	"agentgo/internal/workflow"
)

// scriptedEngine plays a canned coding session.
type scriptedEngine struct{ id string }

func (e scriptedEngine) ID() string { return e.id }
func (e scriptedEngine) Run(_ context.Context, req engine.RunRequest, em engine.Emitter) (engine.RunResult, error) {
	em.ToolCall("c1", "edit", `{}`)
	em.FileChange("a.go", "v0", "v1", "")
	em.FileChange("b.go", "", "new", "")
	em.FileChange("a.go", "v1", "v2", "") // second edit of the same file
	em.ToolCall("c2", "bash", `{}`)
	return engine.RunResult{Messages: []engine.Message{{Role: "assistant", Type: "text", Content: "done in " + req.Workspace}}}, nil
}

func TestCodingAgentRunFoldsEvents(t *testing.T) {
	rt := &Runtime{engineRouter: engine.NewRouter()}
	rt.engineRouter.Register(scriptedEngine{id: "crush"})
	res, err := rt.codingAgentRun(context.Background(), workflow.CodingRequest{Prompt: "go", WorkDir: "C:/w", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != "done in C:/w" || res.ToolCalls != 2 || res.Engine != "crush" {
		t.Fatalf("res = %+v", res)
	}
	if len(res.ChangedFiles) != 2 || res.ChangedFiles[0].Path != "a.go" {
		t.Fatalf("changes = %+v", res.ChangedFiles)
	}
	// a.go: first "before" kept, latest "after" wins; order of first touch preserved.
	if a := res.ChangedFiles[0]; a.Before != "v0" || a.After != "v2" {
		t.Fatalf("a.go = %+v", a)
	}
}

func TestCodingAgentRunWithoutEngine(t *testing.T) {
	_, err := (&Runtime{}).codingAgentRun(context.Background(), workflow.CodingRequest{Prompt: "x", WorkDir: "C:/w"})
	if err == nil || !strings.Contains(err.Error(), crushEnvExe) {
		t.Fatalf("err = %v", err)
	}
	// Router present but crush not registered -> a clear router error, not a panic.
	rt := &Runtime{engineRouter: engine.NewRouter()}
	rt.engineRouter.Register(scriptedEngine{id: "other"})
	res, err := rt.codingAgentRun(context.Background(), workflow.CodingRequest{Prompt: "x", WorkDir: "C:/w"})
	if err == nil {
		t.Fatalf("expected error for unknown engine, got %+v", res)
	}
}
