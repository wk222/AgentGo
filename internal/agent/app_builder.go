package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"

	"agentgo/internal/apps"
	"agentgo/internal/governance"
)

var appBuilderToolAllow = map[string]bool{
	"update_inner_app_file": true,
	"read_inner_app_file":   true,
	"list_inner_app_files":  true,
	"verify_inner_app":      true,
}

func (r *Runner) appBuilderTools() []tool.BaseTool {
	if r.toolReg == nil {
		return nil
	}
	var out []tool.BaseTool
	for _, t := range r.toolReg.GetAllTools() {
		info, err := t.Info(context.Background())
		if err != nil || info == nil {
			continue
		}
		if appBuilderToolAllow[info.Name] {
			out = append(out, t)
		}
	}
	return out
}

func (r *Runner) buildAppBuilderAgent(ctx context.Context, cfg LLMSettings) (adk.Agent, error) {
	toolList := r.appBuilderTools()
	if len(toolList) == 0 {
		return nil, fmt.Errorf("app_builder: no file tools registered")
	}
	return r.buildChatModelAgentWithTools(ctx, cfg,
		"app_builder",
		"在严格沙箱内生成并修复 Inner App 的 HTML/CSS/JS（仅文件工具）",
		`你是 App Builder 子智能体。规则：
1. 只用 update_inner_app_file / read_inner_app_file / list_inner_app_files / verify_inner_app
2. index.html 必须包含 /agentgo-app-helpers.js，且在 static/app.js 之前
3. 每次修改后调用 verify_inner_app，直到无 critical 且 score>=70
4. 不要调用 bash、workflow、register 其它工具
5. 完成后用中文简短汇报修改了哪些文件`,
		toolList, 14, false,
	)
}

// RunAppBuilderAgent runs the isolated app_builder ADK agent for one task prompt.
func (r *Runner) RunAppBuilderAgent(ctx context.Context, cfg LLMSettings, sessionID, userText string) (content string, err error) {
	if r.cpStore == nil {
		return "", fmt.Errorf("checkpoint store unavailable")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return "", fmt.Errorf("LLM API key required for app_builder")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "app_builder"
	}
	ctx = WithSessionMode(WithSessionID(ctx, sessionID), SessionMode{
		Profile: ModeAssistant, Canvas: CanvasDeep,
	})

	ag, err := r.buildAppBuilderAgent(ctx, cfg)
	if err != nil {
		return "", err
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           ag,
		CheckPointStore: r.cpStore,
		EnableStreaming: false,
	})
	iter := runner.Query(ctx, userText, r.queryOptions(ctx, sessionID)...)
	out, _, _, err := r.drainADKEvents(ctx, iter, nil)
	return strings.TrimSpace(out), err
}

// BuildInnerAppFull: scaffold + auto-fix + optional app_builder LLM + final verify.
func (r *Runner) BuildInnerAppFull(ctx context.Context, cfg LLMSettings, sessionID string, b *apps.IterativeBuilder, opt apps.IterativeBuildOptions) apps.IterativeBuildResult {
	if b == nil {
		return apps.IterativeBuildResult{Success: false, Error: "iterative builder nil"}
	}
	out := b.Build(ctx, opt)
	if !out.Scaffold.Success {
		return out
	}
	if out.Success {
		out.Message = "built (deterministic only)"
		return out
	}
	if strings.TrimSpace(cfg.APIKey) == "" || r == nil {
		return out
	}

	name := out.AppName
	task := fmt.Sprintf(
		"应用名 %q，模式 %s。产品描述：\n%s\n\n当前 verify 未通过。请读取相关文件，用 update_inner_app_file 修复，并反复 verify_inner_app 直到通过。",
		name, firstNonEmpty(opt.Mode, "static"), strings.TrimSpace(opt.Description),
	)
	if len(out.FinalVerify.Issues) > 0 {
		ib, _ := json.Marshal(out.FinalVerify.Issues)
		task += "\n\n当前问题 JSON：\n" + string(ib)
	}
	sid := sessionID
	if sid == "" {
		sid = "app_builder"
	}
	sid = sid + ":build:" + name
	ctx = governance.WithSubagentDepth(ctx, governance.SubagentDepth(ctx)+1)

	summary, err := r.RunAppBuilderAgent(ctx, cfg, sid, task)
	if err != nil {
		out.Error = "app_builder: " + err.Error()
		return out
	}
	if summary != "" {
		out.Repairs = append(out.Repairs, "app_builder_agent: "+truncate(summary, 200))
	}

	final := apps.VerifyBundle(ctx, b.Scaffolder.AppsRoot, name, b.Pinger)
	out.FinalVerify = final
	out.Success = final.Success
	if final.Success {
		out.Message = "built with app_builder agent"
	} else {
		out.Message = "scaffold ok; app_builder ran but verify still failing"
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
