package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/compose"

	"agentgo/internal/governance"
)

// CodingRequest is a coding-agent task handed to the host.
type CodingRequest struct {
	Prompt    string
	WorkDir   string // empty = host default workspace
	SessionID string // stable id lets the engine keep context across nodes
	Engine    string // empty = host default coding engine

	// CanPause says the run can be checkpointed, so the host may stop with a
	// *CodingPause when a tool needs approval instead of failing.
	CanPause bool
	// Resume continues a run that stopped on a *CodingPause once the approval
	// has been granted. Prompt is not re-sent.
	Resume *CodingResume
}

// CodingPause is returned by the host's Run when the agent stopped because a
// tool needs approval. The node turns it into a workflow interrupt (HITL), and
// the host gets Host/Calls/Changes back in CodingResume once the user decides.
type CodingPause struct {
	ApprovalID string
	ToolName   string
	Arguments  string
	// Host is opaque to the workflow: whatever the host needs to continue the run.
	Host map[string]string
	// Work done before the pause, carried so the final result covers the whole node.
	Calls   int
	Changes []CodingChange
}

func (p *CodingPause) Error() string {
	return fmt.Sprintf("coding agent paused: %s needs approval (%s)", p.ToolName, p.ApprovalID)
}

// CodingResume is what the host gets back after the approval was granted.
type CodingResume struct {
	ApprovalID string
	ToolName   string
	Arguments  string // the arguments to run with; may have been edited by the approver
	Host       map[string]string
	Calls      int
	Changes    []CodingChange
}

// codingAgentState is the node's persisted interrupt state (compose checkpoint).
type codingAgentState struct {
	ApprovalID string
	ToolName   string
	Arguments  string
	Host       map[string]string
	Calls      int
	Changes    []CodingChange
}

func (s codingAgentState) info() map[string]any {
	// The approval_id form is routed as a tool approval, so the same dialog and
	// the same resolve path serve a pause inside a coding agent.
	return map[string]any{
		"kind": "coding_agent", "approval_id": s.ApprovalID, "tool": s.ToolName, "arguments": s.Arguments,
	}
}

// resumeApproval reads the approval decision handed to compose.ResumeWithData
// (a value or a pointer; eino's GetResumeContext is an exact type assertion).
func resumeApproval(ctx context.Context) (governance.ResumePayload, bool) {
	if ok, has, p := compose.GetResumeContext[governance.ResumePayload](ctx); ok && has {
		return p, true
	}
	if ok, has, p := compose.GetResumeContext[*governance.ResumePayload](ctx); ok && has && p != nil {
		return *p, true
	}
	return governance.ResumePayload{}, false
}

// CodingChange is one file touched by the coding agent.
type CodingChange struct {
	Path   string `json:"path"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// CodingResult is what a CodingAgent node produces.
type CodingResult struct {
	Summary      string         `json:"summary"`
	ChangedFiles []CodingChange `json:"changed_files"`
	ToolCalls    int            `json:"tool_calls"`
	Engine       string         `json:"engine,omitempty"`
}

// CodingAgentNodeExecutor delegates a task to a coding agent and returns a JSON
// document {summary, changed_files, tool_calls, engine} that downstream nodes
// can read (e.g. via a json node), and sets Vars "<nodeID>.summary".
//
// It is not built in: the plugin that owns a coding engine registers it through
// RegisterNodeExecutor, supplying Run.
//
// Config: work_dir, session_id, engine. Prompt supports the usual {{input}} /
// {{last}} template variables.
type CodingAgentNodeExecutor struct {
	Run func(ctx context.Context, req CodingRequest) (CodingResult, error)
}

func (e *CodingAgentNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	prompt := n.Prompt
	if strings.TrimSpace(prompt) == "" {
		prompt = last
	}
	prompt = strings.TrimSpace(expandTemplateVars(prompt, input, last, rc.Vars))
	// A node resumed after an approval continues the paused run and needs no
	// prompt (the upstream input is not replayed on resume).
	paused, hasPause, pausedState := compose.GetInterruptState[codingAgentState](ctx)
	resuming := paused && hasPause
	if prompt == "" && !resuming {
		return "", fmt.Errorf("coding_agent node %s: empty task", n.ID)
	}
	if e.Run == nil {
		return "", fmt.Errorf("coding_agent node %s: no coding engine configured", n.ID)
	}
	req := CodingRequest{Prompt: prompt, SessionID: "workflow:" + rc.RunID + ":" + n.ID, CanPause: rc.CheckPointStore != nil}
	cfgStr := func(k string) string {
		if n.Config == nil {
			return ""
		}
		s, _ := n.Config[k].(string)
		return strings.TrimSpace(expandTemplateVars(s, input, last, rc.Vars))
	}
	req.WorkDir = cfgStr("work_dir")
	req.Engine = cfgStr("engine")
	if s := cfgStr("session_id"); s != "" {
		req.SessionID = s
	}

	// Back from an approval pause: continue the paused run, or stop if refused.
	if resuming {
		st := pausedState
		rp, ok := resumeApproval(ctx)
		if !ok {
			// Another interrupt in the graph is being resumed; stay paused.
			return "", compose.StatefulInterrupt(ctx, st.info(), st)
		}
		if !rp.Approved {
			return "", fmt.Errorf("coding_agent node %s: %s was not approved", n.ID, st.ToolName)
		}
		args := st.Arguments
		if rp.Arguments != "" {
			args = rp.Arguments
		}
		req.Resume = &CodingResume{
			ApprovalID: st.ApprovalID, ToolName: st.ToolName, Arguments: args,
			Host: st.Host, Calls: st.Calls, Changes: st.Changes,
		}
	}

	res, err := e.Run(ctx, req)
	var pause *CodingPause
	if errors.As(err, &pause) {
		st := codingAgentState{
			ApprovalID: pause.ApprovalID, ToolName: pause.ToolName, Arguments: pause.Arguments,
			Host: pause.Host, Calls: pause.Calls, Changes: pause.Changes,
		}
		return "", compose.StatefulInterrupt(ctx, st.info(), st)
	}
	if err != nil {
		return "", err
	}
	if res.ChangedFiles == nil {
		res.ChangedFiles = []CodingChange{}
	}
	if rc.Vars != nil {
		rc.Vars[n.ID+".summary"] = res.Summary
	}
	b, err := json.Marshal(res)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
