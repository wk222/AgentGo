package bridge

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

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

// codingChangeSink folds the event stream into the workflow node result.
//
// The Eino engine reports tool calls, not file changes, so the sink snapshots a
// file-changing tool's target when the call starts and diffs it when the call
// finishes (the same capture the Crush TUI adapter uses). Engines that do emit
// file_change events are folded in as well.
type codingChangeSink struct {
	workDir string

	mu       sync.Mutex
	order    []string
	changes  map[string]*workflow.CodingChange
	calls    int
	pre      map[string]fileSnap // call id -> target before the call
	trees    map[string]treeSnap // call id -> workspace before a shell call
	approval struct{ id, tool, args string }
}

// seed starts the sink from the work done before an approval pause.
func (c *codingChangeSink) seed(calls int, changes []workflow.CodingChange) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = calls
	for _, ch := range changes {
		c.record(ch.Path, ch.Before, ch.After)
	}
}

func (c *codingChangeSink) changeList() []workflow.CodingChange {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]workflow.CodingChange, 0, len(c.order))
	for _, p := range c.order {
		out = append(out, *c.changes[p])
	}
	return out
}

// record must be called with c.mu held.
func (c *codingChangeSink) record(path, before, after string) {
	if path == "" {
		return
	}
	if ch, ok := c.changes[path]; ok {
		ch.After = after // keep the first "before", track the latest "after"
		return
	}
	if c.changes == nil {
		c.changes = map[string]*workflow.CodingChange{}
	}
	c.changes[path] = &workflow.CodingChange{Path: path, Before: before, After: after}
	c.order = append(c.order, path)
}

// watchLocked snapshots what a starting tool call may change.
func (c *codingChangeSink) watchLocked(id, name, args string) {
	if id == "" || c.workDir == "" {
		return
	}
	if snap, ok := snapshotBefore(c.workDir, name, args); ok && snap.ok {
		if c.pre == nil {
			c.pre = map[string]fileSnap{}
		}
		c.pre[id] = snap
	}
	if name == shellTool {
		if c.trees == nil {
			c.trees = map[string]treeSnap{}
		}
		c.trees[id] = scanTree(c.workDir)
	}
}

// settleLocked turns the snapshots of a finished call into recorded changes.
func (c *codingChangeSink) settleLocked(id string, isErr bool) {
	if snap, ok := c.pre[id]; ok {
		delete(c.pre, id)
		if !isErr {
			if after, ok := readSnapshot(snap.path); ok && after != snap.before {
				c.record(snap.path, snap.before, after)
			}
		}
	}
	if before, ok := c.trees[id]; ok {
		delete(c.trees, id)
		for _, d := range diffTrees(before, scanTree(c.workDir)) {
			c.record(d.path, d.before, d.after)
		}
	}
}

// watch/settle bracket a call the engine will not report itself (the tool that
// an approval is about to release).
func (c *codingChangeSink) watch(id, name, args string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.watchLocked(id, name, args)
}

func (c *codingChangeSink) settle(id string, isErr bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settleLocked(id, isErr)
}

func (c *codingChangeSink) Emit(e engine.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	str := func(k string) string { s, _ := e.Payload[k].(string); return s }
	switch e.Type {
	case engine.EventToolCall:
		c.calls++
		c.watchLocked(str("id"), str("name"), str("arguments"))
	case engine.EventToolResult:
		isErr, _ := e.Payload["is_error"].(bool)
		c.settleLocked(str("id"), isErr)
	case engine.EventFileChange:
		c.record(str("path"), str("before"), str("after"))
	case engine.EventApprovalRequest:
		c.approval.id, c.approval.tool, c.approval.args = str("approval_id"), str("tool_name"), str("arguments")
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
	sink := &codingChangeSink{workDir: dir}
	out := workflow.CodingResult{Engine: id}
	finish := func() {
		out.ToolCalls = sink.calls
		out.ChangedFiles = sink.changeList()
	}

	var pending *codingPending
	if req.Resume != nil {
		sink.seed(req.Resume.Calls, req.Resume.Changes)
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
			pending = &codingPending{id: sink.approval.id, tool: sink.approval.tool, args: sink.approval.args}
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
func (r *Runtime) pendingRunFor(approvalID string) (pendingRun, bool) {
	if r == nil || r.pending == nil || approvalID == "" {
		return pendingRun{}, false
	}
	return r.pending.Get(approvalID)
}

// hostOf is what the node keeps across the pause to continue the agent run.
func hostOf(pr pendingRun) map[string]string {
	return map[string]string{
		"session_id": pr.SessionID, "user_text": pr.UserText, "interrupt_id": pr.InterruptID,
	}
}

// continueCodingRun resumes the agent run that was stopped for approval and
// reports what it produced, or the next approval it stopped on.
func (r *Runtime) continueCodingRun(ctx context.Context, sink *codingChangeSink, engineID string, rs *workflow.CodingResume) (string, *codingPending, error) {
	pr := pendingRun{
		SessionID: rs.Host["session_id"], UserText: rs.Host["user_text"], InterruptID: rs.Host["interrupt_id"],
		ToolName: rs.ToolName, Arguments: rs.Arguments, ApprovalID: rs.ApprovalID,
	}
	em, _ := engine.ResumeEmitter(sink, "", pr.SessionID, engineID)
	rctx, cancel := context.WithTimeout(bindEngineObservers(ctx, em), engineRunTimeout)
	defer cancel()

	// The released tool runs inside the resume without a fresh tool-call event,
	// so snapshot its target here.
	synth := "resume:" + rs.ApprovalID
	sink.watch(synth, rs.ToolName, rs.Arguments)
	res, err := r.resumeAgent(rctx, pr, &governance.ResumePayload{Approved: true, Arguments: rs.Arguments})
	if err == nil && res == nil {
		err = fmt.Errorf("resume returned no result")
	}
	if err != nil {
		sink.settle(synth, true)
		return "", nil, err
	}
	// The released tool has run by now, even if the agent then stopped on the
	// next tool that needs approval.
	sink.settle(synth, false)
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
	next.host = hostOf(pendingRun{SessionID: pr.SessionID, UserText: pr.UserText, InterruptID: p.InterruptID})
	return res.Content, next, nil
}
