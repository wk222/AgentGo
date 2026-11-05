package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/acpclient"
	"agentgo/internal/config"
)

const (
	maxExternalText  = 30000
	maxExternalTools = 40
)

type externalAgentInput struct {
	Action         string `json:"action,omitempty" jsonschema:"description=Action: 'run' (default)\\, 'list' (agents and open sessions) or 'close' (end an open session)"`
	Agent          string `json:"agent,omitempty" jsonschema:"description=Which agent: claude\\, codex\\, gemini\\, qwen\\, opencode\\, hermes\\, openclaw\\, codebuddy or one the user configured. Use action=list to see which are installed"`
	Prompt         string `json:"prompt,omitempty" jsonschema:"description=The task for that agent\\, self-contained: it knows nothing of this conversation. Say what to do and what a good answer looks like"`
	Cwd            string `json:"cwd,omitempty" jsonschema:"description=Working directory for the agent (inside the workspace). Default: the workspace root"`
	Permission     string `json:"permission,omitempty" jsonschema:"description=What the agent may do without asking: 'read_only' (default: read\\, search\\, analyse)\\, 'edit' (also change files) or 'full' (also run commands). Start with read_only"`
	Session        string `json:"session,omitempty" jsonschema:"description=A label that keeps the agent's work going between calls: a follow-up prompt with the same label continues it\\, even after the agent was stopped (idle\\, restart) if that agent can load sessions. Omit for a one-off. action=close ends it for good"`
	TimeoutMinutes int    `json:"timeout_minutes,omitempty" jsonschema:"description=Give up after this many minutes (default and max set by the user's settings)"`
}

type externalAgentInfo struct {
	ID        string `json:"id"`
	Installed bool   `json:"installed"`
	Note      string `json:"note,omitempty"`
}

type externalAgentOutput struct {
	Success    bool                    `json:"success"`
	Action     string                  `json:"action"`
	Agent      string                  `json:"agent,omitempty"`
	Session    string                  `json:"session,omitempty"`
	Resumed    bool                    `json:"resumed,omitempty"` // this call carried on a session the agent had before
	StopReason string                  `json:"stop_reason,omitempty"`
	Text       string                  `json:"text,omitempty"`
	Tools      []acpclient.ToolCall    `json:"tools,omitempty"`
	Denied     []string                `json:"denied,omitempty"`
	Agents     []externalAgentInfo     `json:"agents,omitempty"`
	Sessions   []acpclient.SessionInfo `json:"sessions,omitempty"`
	Message    string                  `json:"message,omitempty"`
	Error      string                  `json:"error,omitempty"`
}

// RegisterExternalAgentTool registers `external_agent`: hand a task to another coding agent
// (Claude Code, Codex, Gemini CLI, OpenCode, …) over the Agent Client Protocol and read back what
// it did. It is how AgentGo works alongside the agents a user already has installed.
func RegisterExternalAgentTool(r *Registry, workspaceRoot string) error {
	t, err := utils.InferTool("external_agent",
		"Delegate a task to ANOTHER coding agent installed on this machine (Claude Code, Codex, Gemini CLI, Qwen Code, OpenCode, …) and get its answer back. "+
			"Use it for a second opinion from a different model, for work that agent is better at, or to run a long independent task. "+
			"It runs in the workspace with its own tools and its own login; by default it may only read (permission=read_only). "+
			"Give it a self-contained prompt. With a session label it stays open so you can send follow-ups, and the same label carries on later even after the agent was stopped.",
		func(ctx context.Context, in externalAgentInput) (externalAgentOutput, error) {
			action := strings.ToLower(strings.TrimSpace(in.Action))
			if action == "" {
				action = "run"
			}
			custom, err := acpclient.ParseCustom(config.ExternalAgents.Get())
			if err != nil {
				return externalAgentOutput{Action: action, Error: err.Error()}, nil
			}
			switch action {
			case "list":
				out := externalAgentOutput{Success: true, Action: action, Sessions: acpclient.Default.List()}
				for _, p := range acpclient.Agents(custom) {
					out.Agents = append(out.Agents, externalAgentInfo{ID: p.ID, Installed: p.Installed(), Note: p.Note})
				}
				for _, s := range out.Sessions {
					if !s.Live {
						out.Message = "live=false 的会话 agent 已停止，但用同一个 session 标签再 run 会接着它之前的会话继续；不再需要时 action=close。"
						break
					}
				}
				return out, nil
			case "close":
				label := strings.TrimSpace(in.Session)
				if label == "" {
					return externalAgentOutput{Action: action, Error: "session is required"}, nil
				}
				if !acpclient.Default.Close(label) {
					return externalAgentOutput{Action: action, Error: fmt.Sprintf("no session %q (open or saved)", label)}, nil
				}
				return externalAgentOutput{Success: true, Action: action, Session: label, Message: "已关闭，这个标签之后从头开始"}, nil
			case "run":
				return runExternalAgent(ctx, in, custom, workspaceRoot), nil
			}
			return externalAgentOutput{Action: action, Error: fmt.Sprintf("unknown action %q, use run/list/close", in.Action)}, nil
		},
	)
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}

func runExternalAgent(ctx context.Context, in externalAgentInput, custom map[string]string, workspaceRoot string) externalAgentOutput {
	fail := func(format string, a ...any) externalAgentOutput {
		return externalAgentOutput{Action: "run", Agent: in.Agent, Error: fmt.Sprintf(format, a...)}
	}
	prompt := strings.TrimSpace(in.Prompt)
	if strings.TrimSpace(in.Agent) == "" || prompt == "" {
		return fail("agent and prompt are required (action=list shows the agents)")
	}
	preset, err := acpclient.Lookup(in.Agent, custom)
	if err != nil {
		return fail("%v", err)
	}
	if !preset.Installed() {
		return fail("%s is not installed: %q was not found on PATH (%s)", preset.ID, preset.Binary, preset.Note)
	}
	perm, err := acpclient.ParsePermission(in.Permission)
	if err != nil {
		return fail("%v", err)
	}
	cwd, err := externalCwd(workspaceRoot, in.Cwd)
	if err != nil {
		return fail("%v", err)
	}

	limit := time.Duration(clampMinutes(config.ExternalAgentMinutes.Get(), 20)) * time.Minute
	if m := in.TimeoutMinutes; m > 0 {
		limit = min(time.Duration(m)*time.Minute, limit)
	}
	rctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	label := strings.TrimSpace(in.Session)
	turn, err := acpclient.Default.Run(rctx, label, preset.ID,
		acpclient.Config{Name: preset.ID, Command: preset.Command, Cwd: cwd, Permission: perm}, prompt, nil)
	if err != nil {
		return fail("%v", err)
	}
	out := externalAgentOutput{Success: true, Action: "run", Agent: preset.ID, Session: label, StopReason: turn.StopReason,
		Text: capText(turn.Text, maxExternalText), Denied: turn.Denied}
	out.Tools = turn.Tools
	if len(out.Tools) > maxExternalTools {
		out.Tools = out.Tools[len(out.Tools)-maxExternalTools:]
	}
	switch {
	case rctx.Err() != nil && ctx.Err() == nil:
		out.Message = fmt.Sprintf("超过 %d 分钟，已停止；上面是它到那时为止写出的内容。", int(limit/time.Minute))
	case turn.StopReason == "cancelled":
		out.Message = "已被取消；上面是它到那时为止写出的内容。"
	case len(turn.Denied) > 0:
		out.Message = fmt.Sprintf("它想做的 %d 件事被 permission=%s 拒绝了（见 denied）。需要时让用户确认后用 edit（改文件）或 full（运行命令）重试。", len(turn.Denied), perm)
	}
	if strings.TrimSpace(out.Text) == "" && out.Message == "" {
		out.Message = "它没有返回文字；看 tools 了解它做了什么。"
	}
	// How the session began goes after the checks above, which look at whether anything else was said.
	switch {
	case turn.Resumed:
		out.Resumed = true
		out.Message = joinNotes(out.Message, fmt.Sprintf("已接着标签 %q 之前的会话继续（agent 自己保存的记录）。", label))
	case turn.ResumeNote != "":
		out.Message = joinNotes(out.Message, fmt.Sprintf("想接着标签 %q 之前的会话没成功（%s），这次是新开的会话，它不记得之前的内容。", label, turn.ResumeNote))
	}
	return out
}

func joinNotes(a, b string) string {
	if a == "" {
		return b
	}
	return a + " " + b
}

// externalCwd resolves the agent's working directory and keeps it inside the workspace.
func externalCwd(root, given string) (string, error) {
	root = strings.TrimSpace(root)
	given = strings.TrimSpace(given)
	if root == "" {
		if given == "" {
			return os.Getwd()
		}
		return filepath.Abs(given)
	}
	dir := root
	if given != "" {
		dir = given
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}
	}
	dir = filepath.Clean(dir)
	rel, err := filepath.Rel(filepath.Clean(root), dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("cwd %q is outside the workspace %q", given, root)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("cwd %q is not a directory", dir)
	}
	return dir, nil
}

func clampMinutes(v, def int) int {
	if v <= 0 {
		return def
	}
	return min(v, 120)
}

func capText(s string, max int) string {
	if rs := []rune(s); len(rs) > max {
		return string(rs[:max]) + "\n…（输出已截断）"
	}
	return s
}
