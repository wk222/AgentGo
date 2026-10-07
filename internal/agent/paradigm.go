package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"gopkg.in/yaml.v3"
)

// DefaultParadigmYAML is the fallback default paradigm configuration.
const DefaultParadigmYAML = `name: "default"
description: "default agentgo execution paradigm"
loop_end_max_retry: 3
hooks:
  on_build_system_prompt:
    - file_operation:
        path: "@RULES"
        action: "read"
    - file_operation:
        path: "@SOUL"
        action: "read"
    - file_operation:
        path: "@MEMORY"
        action: "read"
    - memo_injection:
        type: "full"
        count: 10
  on_loop_start:
    - injection:
        content: "如遇复杂任务，请先编排好主线路的执行计划，先规划TODO后执行。"
  on_loop_epoch:
    - injection:
        delay: 5
        content: "[系统周期提醒] 在执行任务前可使用 Search 工具或记忆系统搜索有无对应的能力与历史经验。"
    - injection:
        interval: 10
        content: "[系统周期提醒] 请随时按进度更新主路规划或对齐需求，防止执行发散跑偏。"
  on_loop_end:
    - tool_use_check:
        name: "remember"
        failed_prompt: "本轮对话若产生有价值经验，请调用 remember 工具归档；如无需更新记忆，可直接向用户汇报。"
  on_tool_calling:
    - tool_use_check:
        name: "vision_advisor"
        successed_prompt: "已触发视觉顾问。请重点核对 UI 坐标、对齐与异常状态。"
`

// ParadigmAction represents an action item inside a hook stage.
type ParadigmAction struct {
	FileOperation *struct {
		Path         string `yaml:"path" json:"path"`
		Action       string `yaml:"action" json:"action"`
		FailedPrompt string `yaml:"failed_prompt" json:"failed_prompt"`
	} `yaml:"file_operation,omitempty" json:"file_operation,omitempty"`

	Injection *struct {
		Delay    *int   `yaml:"delay,omitempty" json:"delay,omitempty"`
		Interval *int   `yaml:"interval,omitempty" json:"interval,omitempty"`
		Content  string `yaml:"content" json:"content"`
	} `yaml:"injection,omitempty" json:"injection,omitempty"`

	MemoInjection *struct {
		Type  string `yaml:"type" json:"type"`
		Count int    `yaml:"count" json:"count"`
	} `yaml:"memo_injection,omitempty" json:"memo_injection,omitempty"`

	ToolUseCheck *struct {
		Name            string `yaml:"name" json:"name"`
		FailedPrompt    string `yaml:"failed_prompt,omitempty" json:"failed_prompt,omitempty"`
		SuccessedPrompt string `yaml:"successed_prompt,omitempty" json:"successed_prompt,omitempty"`
	} `yaml:"tool_use_check,omitempty" json:"tool_use_check,omitempty"`
}

// ParadigmConfig represents the top-level PARADIGM.yaml structure.
type ParadigmConfig struct {
	Name            string                      `yaml:"name" json:"name"`
	Description     string                      `yaml:"description" json:"description"`
	LoopEndMaxRetry int                         `yaml:"loop_end_max_retry" json:"loop_end_max_retry"`
	Hooks           map[string][]ParadigmAction `yaml:"hooks" json:"hooks"`
}

// ParadigmEngine executes lifecycle rules and prompts driven by PARADIGM.yaml,
// ported and enhanced from PurrCat's Paradigm / HookHandler architecture.
type ParadigmEngine struct {
	mu           sync.RWMutex
	config       ParadigmConfig
	configPath   string
	pathAliases  map[string]string
	recentMemos  []string
	memoMu       sync.RWMutex
}

// NewParadigmEngine creates a ParadigmEngine instance.
func NewParadigmEngine(configPath string, aliases map[string]string) *ParadigmEngine {
	if aliases == nil {
		aliases = make(map[string]string)
	}

	pe := &ParadigmEngine{
		configPath:  configPath,
		pathAliases: aliases,
		recentMemos: make([]string, 0, 10),
	}
	pe.LoadConfig()
	return pe
}

// LoadConfig loads PARADIGM.yaml from disk or initializes the default configuration.
func (pe *ParadigmEngine) LoadConfig() {
	pe.mu.Lock()
	defer pe.mu.Unlock()

	var cfg ParadigmConfig
	if pe.configPath != "" {
		if data, err := os.ReadFile(pe.configPath); err == nil {
			if err := yaml.Unmarshal(data, &cfg); err == nil && len(cfg.Hooks) > 0 {
				pe.config = cfg
				return
			}
		}
	}

	// Fallback to default
	_ = yaml.Unmarshal([]byte(DefaultParadigmYAML), &cfg)
	pe.config = cfg
}

// SetAlias registers or updates a path alias (e.g. "@RULES" -> "path/to/RULES.md").
func (pe *ParadigmEngine) SetAlias(alias, path string) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	pe.pathAliases[alias] = path
}

// AddMemo stores a recent interaction memo (kept up to last 10 items).
func (pe *ParadigmEngine) AddMemo(memo string) {
	memo = strings.TrimSpace(memo)
	if memo == "" {
		return
	}
	pe.memoMu.Lock()
	defer pe.memoMu.Unlock()

	if len(pe.recentMemos) >= 10 {
		pe.recentMemos = pe.recentMemos[1:]
	}
	pe.recentMemos = append(pe.recentMemos, memo)
}

func (pe *ParadigmEngine) resolvePath(aliasOrPath string) string {
	if p, ok := pe.pathAliases[aliasOrPath]; ok {
		return p
	}
	return aliasOrPath
}

// OnBuildSystemPrompt executes actions for the "on_build_system_prompt" hook.
func (pe *ParadigmEngine) OnBuildSystemPrompt() []string {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	actions := pe.config.Hooks["on_build_system_prompt"]
	var prompts []string

	for _, act := range actions {
		if act.FileOperation != nil {
			realPath := pe.resolvePath(act.FileOperation.Path)
			if act.FileOperation.Action == "read" {
				if content, err := os.ReadFile(realPath); err == nil && len(content) > 0 {
					prompts = append(prompts, fmt.Sprintf("【系统指令 %s】\n%s", act.FileOperation.Path, string(content)))
				}
			}
		} else if act.MemoInjection != nil {
			pe.memoMu.RLock()
			if len(pe.recentMemos) > 0 {
				memoText := "【短时连续记忆 (最近交互概要)】:\n- " + strings.Join(pe.recentMemos, "\n- ")
				prompts = append(prompts, memoText)
			}
			pe.memoMu.RUnlock()
		} else if act.Injection != nil && act.Injection.Content != "" {
			prompts = append(prompts, act.Injection.Content)
		}
	}

	return prompts
}

// OnLoopStart executes actions for the "on_loop_start" hook.
func (pe *ParadigmEngine) OnLoopStart() []string {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	actions := pe.config.Hooks["on_loop_start"]
	var prompts []string

	for _, act := range actions {
		if act.Injection != nil && act.Injection.Content != "" {
			prompts = append(prompts, act.Injection.Content)
		}
	}
	return prompts
}

// OnLoopEpoch evaluates epoch triggers (delay once, or interval repeating) and returns prompts.
func (pe *ParadigmEngine) OnLoopEpoch(epoch int) []string {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	actions := pe.config.Hooks["on_loop_epoch"]
	var prompts []string

	for _, act := range actions {
		if act.Injection == nil || act.Injection.Content == "" {
			continue
		}
		inj := act.Injection
		// Delay trigger: triggered once when epoch == delay
		if inj.Delay != nil && *inj.Delay == epoch {
			prompts = append(prompts, inj.Content)
			continue
		}
		// Interval trigger: triggered periodically when epoch % interval == 0
		if inj.Interval != nil && *inj.Interval > 0 && epoch > 0 && epoch%*inj.Interval == 0 {
			prompts = append(prompts, inj.Content)
			continue
		}
	}
	return prompts
}

// OnLoopEnd checks whether loop-end validation rules pass.
// Returns (passed, hintPrompt). If false, the agent loop should be nudged with hintPrompt.
func (pe *ParadigmEngine) OnLoopEnd(usedTools []string) (bool, string) {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	actions := pe.config.Hooks["on_loop_end"]
	toolSet := make(map[string]bool)
	for _, t := range usedTools {
		toolSet[t] = true
	}

	for _, act := range actions {
		if act.ToolUseCheck != nil {
			targetTool := act.ToolUseCheck.Name
			if !toolSet[targetTool] && act.ToolUseCheck.FailedPrompt != "" {
				return false, act.ToolUseCheck.FailedPrompt
			}
		}
	}
	return true, ""
}

// OnToolCalling checks for advice/prompts when a specific tool is invoked.
func (pe *ParadigmEngine) OnToolCalling(toolName string) string {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	actions := pe.config.Hooks["on_tool_calling"]
	for _, act := range actions {
		if act.ToolUseCheck != nil && act.ToolUseCheck.Name == toolName {
			return act.ToolUseCheck.SuccessedPrompt
		}
	}
	return ""
}

// ParadigmMiddleware implements Eino's adk.ChatModelAgentMiddleware to dynamically inject
// declarative prompt rules, lifecycle epoch reminders, and memo summaries into the ADK loop.
type ParadigmMiddleware struct {
	adk.BaseChatModelAgentMiddleware
	engine     *ParadigmEngine
	epochMu    sync.Mutex
	epochCount int
	firstRun   bool
}

// NewParadigmMiddleware creates a new ADK middleware wrapping ParadigmEngine.
func NewParadigmMiddleware(engine *ParadigmEngine) *ParadigmMiddleware {
	return &ParadigmMiddleware{
		engine:   engine,
		firstRun: true,
	}
}

// BeforeModelRewriteState injects initial paradigm rules on start and regular epoch hints.
func (m *ParadigmMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if m.engine == nil {
		return ctx, state, nil
	}

	m.epochMu.Lock()
	m.epochCount++
	currentEpoch := m.epochCount
	isFirst := m.firstRun
	m.firstRun = false
	m.epochMu.Unlock()

	var injectedPrompts []string

	// 1. Initial prompt injection (rules, soul, memory, loop start hints)
	if isFirst {
		injectedPrompts = append(injectedPrompts, m.engine.OnBuildSystemPrompt()...)
		injectedPrompts = append(injectedPrompts, m.engine.OnLoopStart()...)
	}

	// 2. Epoch interval reminders (e.g. check plan, search experience)
	injectedPrompts = append(injectedPrompts, m.engine.OnLoopEpoch(currentEpoch)...)

	if len(injectedPrompts) == 0 {
		return ctx, state, nil
	}

	injectionText := strings.Join(injectedPrompts, "\n\n")

	// Inject into System Message or prepend
	injected := false
	for i, msg := range state.Messages {
		if msg.Role == schema.System {
			state.Messages[i].Content = fmt.Sprintf("%s\n\n%s", msg.Content, injectionText)
			injected = true
			break
		}
	}
	if !injected {
		state.Messages = append([]*schema.Message{schema.SystemMessage(injectionText)}, state.Messages...)
	}

	return ctx, state, nil
}

