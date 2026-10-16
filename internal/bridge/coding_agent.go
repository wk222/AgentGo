package bridge

import (
	"agentgo/internal/core"
	"context"
	"fmt"
	"log"
	"strings"

	"agentgo/internal/engine"
	"agentgo/internal/governance"
	"agentgo/internal/plugin"
	"agentgo/internal/workflow"
)

// The coding_agent workflow node runs a coding task on the same Eino engine that
// serves chat, with the in-process Crush tools (codetools plugin: LSP, edit,
// search). There is no second agent loop or second model configuration: the
// former opt-in Crush sidecar engine was removed, and what it did is now this
// plugin plus codetools. "crush" stays as a node-type alias so saved workflows
// keep loading.

// codingAgentNodeTypes are the workflow node types this plugin contributes.
var codingAgentNodeTypes = []string{"coding_agent", "crush"}

// codingAgentPlugin contributes the coding_agent workflow node.
func codingAgentPlugin(rt *Runtime) (plugin.Plugin, bool) {
	if rt == nil {
		return plugin.Plugin{}, false
	}
	return plugin.Plugin{
		Name:   "coding-agent",
		Inject: []string{svcEngines},
		Apply: func(c *plugin.Context) error {
			node := &workflow.CodingAgentNodeExecutor{Run: rt.codingAgentRun}
			for _, typ := range codingAgentNodeTypes {
				dispose, err := workflow.RegisterNodeExecutor(typ, node)
				if err != nil {
					return err
				}
				c.Contribute("workflow_node", typ, dispose)
			}
			return nil
		},
	}, true
}

// ServiceShutdown stops plugins so nothing is orphaned.
func (s *AppService) ServiceShutdown() error {
	s.Close()
	return nil
}

// Close stops every plugin, dependents first (idempotent). Headless hosts should call it on exit.
func (s *AppService) Close() {
	if s.unbindWorkspace != nil {
		s.unbindWorkspace() // a closed service must not be reloaded by a later switch
		s.unbindWorkspace = nil
	}
	// The host is shared with the runtime, so this also stops the runtime's own
	// plugins, dependents first.
	if s.plugins != nil {
		if err := s.plugins.Shutdown(); err != nil {
			log.Printf("[plugin] shutdown: %v", err)
		}
	}
}

// codingAgentRun backs the CodingAgent workflow node.
func (r *Runtime) codingAgentRun(ctx context.Context, req workflow.CodingRequest) (workflow.CodingResult, error) {
	router := r.engineRouter
	if router == nil {
		return workflow.CodingResult{}, fmt.Errorf("coding engine unavailable: the engine router is not running")
	}
	id := req.Engine
	if id == "" || strings.EqualFold(id, "crush") { // "crush" is the legacy engine name in saved workflows
		id = EinoEngineID
	}
	dir := req.WorkDir
	if dir == "" {
		dir = r.WorkspaceRoot()
	}
	// The Eino engine edits through tools bound to the active workspace. Running
	// it for another directory would silently change the wrong tree, so refuse.
	if id == EinoEngineID {
		if active := r.WorkspaceRoot(); active != "" && dir != "" && !samePath(dir, active) {
			return workflow.CodingResult{Engine: id}, fmt.Errorf(
				"coding agent runs in the active workspace %s, but the node asks for %s: switch the workspace first", active, dir)
		}
	}
	sink := &core.ChangeSink{WorkDir: dir, EditTools: editToolNames}
	out := workflow.CodingResult{Engine: id}
	finish := func() {
		out.ToolCalls = sink.Calls()
		out.ChangedFiles = sink.Changes()
	}

	var pending *codingPending
	if req.Resume != nil {
		sink.Seed(req.Resume.Calls, req.Resume.Changes)
		summary, p, err := r.continueCodingRun(ctx, sink, id, req.Resume)
		out.Summary, pending = summary, p
		if err != nil {
			finish()
			return out, fmt.Errorf("coding agent (%s): %w", id, err)
		}
	} else {
		res := router.Run(ctx, engine.RunRequest{
			Engine: id, SessionID: req.SessionID, Input: req.Prompt, Workspace: dir,
		}, sink)
		if len(res.Messages) > 0 {
			out.Summary = res.Messages[len(res.Messages)-1].Content
		}
		if res.Error != "" {
			finish()
			return out, fmt.Errorf("coding agent (%s): %s", id, res.Error)
		}
		if res.Pending {
			apID, apTool, apArgs := sink.Approval()
			pending = &codingPending{id: apID, tool: apTool, args: apArgs}
			if ipr, ok := r.pendingRunFor(pending.id); ok {
				pending.host = hostOf(ipr)
			}
		}
	}
	finish()

	if pending == nil {
		return out, nil
	}
	if !req.CanPause || pending.id == "" || pending.host == nil {
		return out, fmt.Errorf("coding agent (%s): waiting for approval, which this run cannot hold "+
			"(workflow checkpointing is off, or the approval was not recorded)", id)
	}
	return out, &workflow.CodingPause{
		ApprovalID: pending.id, ToolName: pending.tool, Arguments: pending.args,
		Host: pending.host, Calls: out.ToolCalls, Changes: out.ChangedFiles,
	}
}

// codingPending is an approval the agent run stopped on.
type codingPending struct {
	id, tool, args string
	host           map[string]string
}

// pendingRunFor reads the agent run that stopped on approvalID. The run stays in
// the store until the workflow interrupt takes the approval over.
func (r *Runtime) pendingRunFor(approvalID string) (core.PendingRun, bool) {
	if r == nil || r.pending == nil || approvalID == "" {
		return core.PendingRun{}, false
	}
	return r.pending.Get(approvalID)
}

// hostOf is what the node keeps across the pause to continue the agent run.
func hostOf(pr core.PendingRun) map[string]string {
	return map[string]string{
		"session_id": pr.SessionID, "user_text": pr.UserText, "interrupt_id": pr.InterruptID,
	}
}

// continueCodingRun resumes the agent run that was stopped for approval and
// reports what it produced, or the next approval it stopped on.
func (r *Runtime) continueCodingRun(ctx context.Context, sink *core.ChangeSink, engineID string, rs *workflow.CodingResume) (string, *codingPending, error) {
	pr := core.PendingRun{
		SessionID: rs.Host["session_id"], UserText: rs.Host["user_text"], InterruptID: rs.Host["interrupt_id"],
		ToolName: rs.ToolName, Arguments: rs.Arguments, ApprovalID: rs.ApprovalID,
	}
	em, _ := engine.ResumeEmitter(sink, "", pr.SessionID, engineID)
	rctx, cancel := context.WithTimeout(bindEngineObservers(ctx, em), engineRunTimeout)
	defer cancel()

	// The released tool runs inside the resume without a fresh tool-call event,
	// so snapshot its target here.
	synth := "resume:" + rs.ApprovalID
	sink.Watch(synth, rs.ToolName, rs.Arguments)
	res, err := r.resumeAgent(rctx, pr, &governance.ResumePayload{Approved: true, Arguments: rs.Arguments})
	if err == nil && res == nil {
		err = fmt.Errorf("resume returned no result")
	}
	if err != nil {
		sink.Settle(synth, true)
		return "", nil, err
	}
	// The released tool has run by now, even if the agent then stopped on the
	// next tool that needs approval.
	sink.Settle(synth, false)
	p := res.PendingApproval
	if p == nil {
		return res.Content, nil, nil
	}
	if p.Question != nil {
		return res.Content, nil, fmt.Errorf("the agent asked a question, which a workflow node cannot answer")
	}
	next := &codingPending{id: p.ApprovalID, tool: p.ToolName, args: p.Arguments}
	if next.id == "" {
		next.id = p.InterruptID
	}
	next.host = hostOf(core.PendingRun{SessionID: pr.SessionID, UserText: pr.UserText, InterruptID: p.InterruptID})
	return res.Content, next, nil
}
