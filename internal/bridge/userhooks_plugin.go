package bridge

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"agentgo/internal/applog"
	"agentgo/internal/codetools"
	"agentgo/internal/config"
	"agentgo/internal/governance"
	"agentgo/internal/plugin"
	"agentgo/internal/userhooks"
)

// userHooksFile is the file of scripts the user lists to run around tool calls
// and messages. It sits in the data directory, next to config.json.
const userHooksFile = "hooks.json"

// skillCredentialsFile 放技能脚本要用的密钥（{"名称":"值"}），在数据目录里。技能只写名称，值在脚本运行时才取。
const skillCredentialsFile = "skill-credentials.json"

// claudeToolAliases lets a hook written for Claude Code or Codex name our tools
// the way those products do: a matcher "Bash" selects execute_bash, "Edit"
// selects code_edit, and so on. Our own names keep working.
var claudeToolAliases = map[string][]string{
	"Bash":      {"execute_bash"},
	"Read":      {codetools.ToolName("view")},
	"Write":     {codetools.ToolName("write")},
	"Edit":      {codetools.ToolName("edit")},
	"MultiEdit": {codetools.ToolName("multiedit")},
	"Glob":      {codetools.ToolName("glob")},
	"Grep":      {codetools.ToolName("grep")},
	"LS":        {codetools.ToolName("ls")},
	"WebSearch": {"web_search"},
}

// userHooksPlugin starts the user's hooks. With AGENTGO_USER_HOOKS=0 nothing is
// registered and a hooks.json is not even read.
func (rt *Runtime) userHooksPlugin() plugin.Plugin {
	return optionalPlugin("userhooks", nil, nil, nil, func(c *plugin.Context) error {
		if !config.UserHooks.On() {
			return nil
		}
		engine := userhooks.New(userhooks.Options{
			Path:       filepath.Join(rt.dataDir, userHooksFile),
			Aliases:    claudeToolAliases,
			CheckEvery: 300 * time.Millisecond,
		})
		rt.setHooksEngine(engine)
		withdraw := governance.RegisterToolHook(userToolHook(engine))
		c.Effect("withdraw user hooks", func() {
			withdraw()
			rt.setHooksEngine(nil) // a stopped plugin must not look as if it were running
		})
		return nil
	})
}

// hooksEngine is the running engine, or nil when hooks are switched off or the
// plugin is stopped. The plugin can be stopped and restarted while the app runs.
func (rt *Runtime) hooksEngine() *userhooks.Engine { return rt.userHooks.Load() }

func (rt *Runtime) setHooksEngine(e *userhooks.Engine) { rt.userHooks.Store(e) }

// userToolHook puts the engine in front of and behind every tool execution.
// governance calls it only for a call that is about to run: one that is still
// waiting for approval, was rejected, or was denied by policy never reaches
// the user's scripts.
func userToolHook(e *userhooks.Engine) governance.ToolHook {
	return governance.ToolHook{
		// The name reaches the model in the refusal ("blocked by user hook: ..."),
		// so it says whose rule this is.
		Name: "user hook",
		Before: func(ctx context.Context, call governance.ToolCall) error {
			out := e.Fire(ctx, userhooks.Event{
				Name: userhooks.PreToolUse, SessionID: call.SessionID, Cwd: call.Workspace,
				ToolName: call.Name, ToolInput: call.Arguments, ToolUseID: call.CallID,
			})
			logHookWarnings(out.Warnings)
			if out.Blocked {
				return errors.New(out.Reason)
			}
			return nil
		},
		After: func(ctx context.Context, call governance.ToolCall, o governance.ToolOutcome) {
			ev := userhooks.Event{
				Name: userhooks.PostToolUse, SessionID: call.SessionID, Cwd: call.Workspace,
				ToolName: call.Name, ToolInput: call.Arguments, ToolUseID: call.CallID, ToolOutput: o.Output,
			}
			if o.Err != nil {
				ev.ToolError = o.Err.Error()
			}
			logHookWarnings(e.Fire(ctx, ev).Warnings)
		},
	}
}

// logHookWarnings writes what went wrong with a script to the log; the same
// text is kept in the engine's run records for the settings page.
func logHookWarnings(warnings []string) {
	if len(warnings) > 0 {
		applog.Warn("user hook: %s", strings.Join(warnings, "; "))
	}
}
