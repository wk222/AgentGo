package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// CodingRequest is a coding-agent task handed to the host (Crush sidecar).
type CodingRequest struct {
	Prompt    string
	WorkDir   string // empty = host default workspace
	SessionID string // stable id lets the engine keep context across nodes
	Engine    string // empty = host default coding engine
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
	if prompt == "" {
		return "", fmt.Errorf("coding_agent node %s: empty task", n.ID)
	}
	if e.Run == nil {
		return "", fmt.Errorf("coding_agent node %s: no coding engine configured", n.ID)
	}
	req := CodingRequest{Prompt: prompt, SessionID: "workflow:" + rc.RunID + ":" + n.ID}
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
	res, err := e.Run(ctx, req)
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
