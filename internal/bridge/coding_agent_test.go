package bridge

import (
	"context"
	"strings"
	"testing"

	"agentgo/internal/capability"
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
	rt.engineRouter.Register(scriptedEngine{id: EinoEngineID})
	res, err := rt.codingAgentRun(context.Background(), workflow.CodingRequest{Prompt: "go", WorkDir: "C:/w", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != "done in C:/w" || res.ToolCalls != 2 || res.Engine != EinoEngineID {
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

// A saved workflow that names the removed Crush engine runs on Eino.
func TestCodingAgentLegacyCrushEngineNameMapsToEino(t *testing.T) {
	rt := &Runtime{engineRouter: engine.NewRouter()}
	rt.engineRouter.Register(scriptedEngine{id: EinoEngineID})
	res, err := rt.codingAgentRun(context.Background(), workflow.CodingRequest{Prompt: "go", Engine: "crush", WorkDir: "C:/w"})
	if err != nil || res.Engine != EinoEngineID {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestCodingAgentRunWithoutRouter(t *testing.T) {
	_, err := (&Runtime{}).codingAgentRun(context.Background(), workflow.CodingRequest{Prompt: "x", WorkDir: "C:/w"})
	if err == nil || !strings.Contains(err.Error(), "router") {
		t.Fatalf("err = %v", err)
	}
	// Router present but the engine is not registered -> a clear router error, not a panic.
	rt := &Runtime{engineRouter: engine.NewRouter()}
	rt.engineRouter.Register(scriptedEngine{id: "other"})
	res, err := rt.codingAgentRun(context.Background(), workflow.CodingRequest{Prompt: "x", WorkDir: "C:/w"})
	if err == nil {
		t.Fatalf("expected error for unknown engine, got %+v", res)
	}
}

// The Eino engine edits the active workspace only; a node asking for another
// directory must fail instead of editing the wrong tree.
func TestCodingAgentRefusesAForeignWorkDir(t *testing.T) {
	active, other := t.TempDir(), t.TempDir()
	rt := &Runtime{engineRouter: engine.NewRouter(), workspace: active}
	rt.engineRouter.Register(scriptedEngine{id: EinoEngineID})
	_, err := rt.codingAgentRun(context.Background(), workflow.CodingRequest{Prompt: "x", WorkDir: other})
	if err == nil || !strings.Contains(err.Error(), "active workspace") {
		t.Fatalf("err = %v", err)
	}
	if _, err := rt.codingAgentRun(context.Background(), workflow.CodingRequest{Prompt: "x", WorkDir: active}); err != nil {
		t.Fatalf("active workspace must be accepted: %v", err)
	}
}

func TestCodingAgentPluginContributesAndRetiresNodes(t *testing.T) {
	bus := capability.NewBus()
	s := NewAppService(&Runtime{capBus: bus})
	defer s.Close()
	if !workflow.NodeTypeAvailable("coding_agent") || !workflow.NodeTypeAvailable("crush") {
		t.Fatal("coding_agent nodes must be contributed by the coding-agent plugin")
	}
	if st, ok := grantStatus(bus, "workflow_node", "coding-agent", "coding_agent"); !ok || st != capability.StatusPublished {
		t.Fatalf("grant: %v %v", st, ok)
	}
	if r := s.StopPlugin("coding-agent"); r["success"] != true {
		t.Fatalf("stop: %v", r)
	}
	if workflow.NodeTypeAvailable("coding_agent") {
		t.Fatal("node type must disappear with the plugin")
	}
	if st, _ := grantStatus(bus, "workflow_node", "coding-agent", "coding_agent"); st != capability.StatusRetired {
		t.Fatalf("grant status after stop = %q", st)
	}
	if r := s.ReloadPlugin("coding-agent"); r["success"] != true || !workflow.NodeTypeAvailable("coding_agent") {
		t.Fatalf("reload: %v", r)
	}
	s.Close()
	s.Close() // idempotent
	if workflow.NodeTypeAvailable("coding_agent") {
		t.Fatal("shutdown left the node behind")
	}
}
