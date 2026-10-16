package bridge

import (
	"agentgo/internal/core"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"agentgo/internal/agent"
	"agentgo/internal/checkpoint"
	"agentgo/internal/engine"
	"agentgo/internal/governance"
	"agentgo/internal/tools"
	"agentgo/internal/workflow"
)

// codingHITL wires a workflow with one coding_agent node to a scripted agent
// that stops for approval twice, so the whole path is exercised: node pause ->
// approval resolved through AppService -> agent resumed -> second pause (chained
// approval) -> second approval -> node result.
type codingHITL struct {
	t     *testing.T
	rt    *Runtime
	s     *AppService
	queue *governance.ApprovalQueue
	ws    string
	def   workflow.Definition
}

const (
	hitlFirstApproval  = "appr_first"
	hitlSecondApproval = "appr_second"
)

func (h *codingHITL) request(id string) {
	h.t.Helper()
	req := governance.NewApprovalRequest("tool_approval", "test", "edit", "approve?")
	req.ID = id
	if err := h.queue.CreateRequest(context.Background(), req); err != nil {
		h.t.Fatal(err)
	}
}

// pausingEngine is the first run: it starts an edit and stops for approval.
type pausingEngine struct{ h *codingHITL }

func (e pausingEngine) ID() string { return EinoEngineID }
func (e pausingEngine) Run(_ context.Context, req engine.RunRequest, em engine.Emitter) (engine.RunResult, error) {
	args := `{"file_path":"a.txt"}`
	em.ToolCall("call1", "code_edit", args)
	e.h.request(hitlFirstApproval)
	em.ApprovalRequest(hitlFirstApproval, "code_edit", args, "approve?")
	// What AppService.registerPending records for a paused chat run.
	e.h.rt.pending.Set(core.PendingRun{
		SessionID: req.SessionID, UserText: req.Input, ToolName: "code_edit", Arguments: args,
		ApprovalID: hitlFirstApproval, InterruptID: "int_first",
	})
	return engine.RunResult{Pending: true}, nil
}

func newCodingHITL(t *testing.T) *codingHITL {
	t.Helper()
	t.Setenv("AGENTGO_WORKFLOW_CHECKPOINT", "1")
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	wfStore, err := workflow.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	cpStore, err := checkpoint.OpenSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := governance.NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })

	h := &codingHITL{t: t, queue: queue, ws: t.TempDir()}
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("v0"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.rt = &Runtime{
		wfStore: wfStore, cpStore: cpStore, approvals: queue,
		toolReg: tools.NewRegistry(), pending: core.NewPendingStore(), workspace: h.ws,
	}
	h.rt.wfExec = h.rt.executeWorkflow
	h.rt.wfResume = h.rt.resumeWorkflow

	h.s = NewAppService(h.rt) // the coding-agent plugin registers the node
	t.Cleanup(h.s.Close)
	if r := h.s.StopPlugin("eino"); r["success"] != true {
		t.Fatalf("stop eino: %v", r)
	}
	h.rt.engineRouter.Register(pausingEngine{h})

	h.def = workflow.Definition{
		ID: "wf_coding_hitl", Name: "coding hitl",
		Nodes: []workflow.Node{
			{ID: "n1", Type: "input"},
			{ID: "n2", Type: "coding_agent", Prompt: "{{input}}"},
			{ID: "n3", Type: "output"},
		},
		Edges: []workflow.Edge{{From: "n1", To: "n2"}, {From: "n2", To: "n3"}},
	}
	if err := wfStore.Save(h.def); err != nil {
		t.Fatal(err)
	}
	return h
}

// start runs the workflow until the node pauses and registers the pause the
// way the IPC layer does.
func (h *codingHITL) start() map[string]any {
	h.t.Helper()
	_, err := h.rt.executeWorkflow(context.Background(), h.def.ID, "fix it")
	ie, ok := workflow.AsInterrupt(err)
	if !ok {
		h.t.Fatalf("expected the coding_agent node to pause, got %v", err)
	}
	resp := h.rt.workflowInterruptResponse(h.def.ID, ie)
	if resp["interrupt_kind"] != string(workflow.InterruptToolApproval) || resp["approval_id"] != hitlFirstApproval {
		h.t.Fatalf("pause = %+v", resp)
	}
	if pr, ok := h.rt.pending.Get(hitlFirstApproval); !ok || pr.ResumeKind != "workflow" {
		h.t.Fatalf("pending = %+v ok=%v", pr, ok)
	}
	return resp
}

func TestCodingAgentNodePausesForApprovalAndChainsApprovals(t *testing.T) {
	h := newCodingHITL(t)
	ctx := context.Background()
	h.start()

	// First approval releases the edit; the agent then stops on a second tool.
	h.rt.resumeFn = func(_ context.Context, pr core.PendingRun, rp *governance.ResumePayload) (*agent.RunResult, error) {
		switch pr.ApprovalID {
		case hitlFirstApproval:
			if pr.SessionID == "" || pr.InterruptID != "int_first" || pr.ToolName != "code_edit" || !rp.Approved {
				t.Errorf("first resume got %+v %+v", pr, rp)
			}
			if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("v1"), 0o644); err != nil {
				t.Error(err)
			}
			h.request(hitlSecondApproval)
			return &agent.RunResult{PendingApproval: &agent.PendingApproval{
				ApprovalID: hitlSecondApproval, InterruptID: "int_second",
				ToolName: "code_write", Arguments: `{"file_path":"b.txt"}`,
			}}, nil
		case hitlSecondApproval:
			if pr.InterruptID != "int_second" || pr.ToolName != "code_write" {
				t.Errorf("second resume got %+v", pr)
			}
			if err := os.WriteFile(filepath.Join(h.ws, "b.txt"), []byte("new"), 0o644); err != nil {
				t.Error(err)
			}
			return &agent.RunResult{Content: "all done"}, nil
		}
		t.Errorf("unexpected approval %q", pr.ApprovalID)
		return nil, nil
	}

	// Approving the first one resumes the workflow, which pauses again: that
	// must be reported as a new pause, not as a failure.
	out := h.s.resolveApproval(ctx, hitlFirstApproval, true, "", "tester", "")
	if out["resume_error"] != nil {
		t.Fatalf("a second pause is not an error: %v", out["resume_error"])
	}
	if out["interrupted"] != true || out["approval_id"] != hitlSecondApproval || out["tool_name"] != "code_write" {
		t.Fatalf("chained pause not reported: %+v", out)
	}
	if pr, ok := h.rt.pending.Get(hitlSecondApproval); !ok || pr.ResumeKind != "workflow" {
		t.Fatalf("second approval not registered for the workflow: %+v ok=%v", pr, ok)
	}

	out = h.s.resolveApproval(ctx, hitlSecondApproval, true, "", "tester", "")
	if out["resume_error"] != nil {
		t.Fatalf("resume_error: %v", out["resume_error"])
	}
	raw, _ := out["workflow_output"].(string)
	var res workflow.CodingResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatalf("workflow_output = %q: %v", raw, err)
	}
	if res.Summary != "all done" {
		t.Errorf("summary = %q", res.Summary)
	}
	if res.ToolCalls != 1 {
		t.Errorf("tool_calls = %d, want the one call made before the first pause", res.ToolCalls)
	}
	byPath := map[string]workflow.CodingChange{}
	for _, c := range res.ChangedFiles {
		byPath[filepath.Base(c.Path)] = c
	}
	if a := byPath["a.txt"]; a.Before != "v0" || a.After != "v1" {
		t.Errorf("a.txt (released by the first approval) = %+v", a)
	}
	if b := byPath["b.txt"]; b.Before != "" || b.After != "new" {
		t.Errorf("b.txt (released by the second approval) = %+v", b)
	}
	if h.rt.pending.Len() != 0 {
		t.Errorf("pending runs left behind: %d", h.rt.pending.Len())
	}
}

func TestCodingAgentNodeFailsWhenApprovalIsRefused(t *testing.T) {
	h := newCodingHITL(t)
	h.start()
	h.rt.resumeFn = func(context.Context, core.PendingRun, *governance.ResumePayload) (*agent.RunResult, error) {
		t.Error("a refused tool must not be released")
		return nil, nil
	}
	out := h.s.resolveApproval(context.Background(), hitlFirstApproval, false, "no", "tester", "")
	if out["success"] != true || out["resume_error"] != nil {
		t.Fatalf("refusal = %+v", out)
	}
	if b, err := os.ReadFile(filepath.Join(h.ws, "a.txt")); err != nil || string(b) != "v0" {
		t.Fatalf("a.txt = %q err=%v", b, err)
	}
}

// Without checkpointing a pause cannot be held: say so instead of hanging.
func TestCodingAgentNodeExplainsWhenItCannotPause(t *testing.T) {
	h := newCodingHITL(t)
	res, err := h.rt.codingAgentRun(context.Background(), workflow.CodingRequest{Prompt: "go", SessionID: "s"})
	if err == nil || !strings.Contains(err.Error(), "waiting for approval") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, ok := err.(*workflow.CodingPause); ok {
		t.Fatal("CanPause was false; the host must not return a pause")
	}
}
