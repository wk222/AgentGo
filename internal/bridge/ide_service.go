package bridge

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// GetSystemStatus returns current model, active mode, workspace root and health state.
func (s *AppService) GetSystemStatus() map[string]any {
	cfg := s.rt.LLMConfig()
	modelName := cfg.Model
	if modelName == "" {
		modelName = "default"
	}

	mode := "coder"
	runner := s.rt.AgentRunner()
	if runner != nil {
		sm := runner.SessionMode()
		if sm.Profile != "" {
			mode = string(sm.Profile)
		}
	}

	return map[string]any{
		"model":          modelName,
		"mode":           mode,
		"workspace":      s.rt.WorkspaceRoot(),
		"active_jobs":    0,
		"memory_enabled": true,
		"status":         "ready",
	}
}

// SwitchIDEProfile switches AgentGo runtime session mode.
func (s *AppService) SwitchIDEProfile(mode string) map[string]any {
	return s.SetSessionMode(mode, "")
}

// TerminalQuickFix analyzes failed terminal execution and suggests corrections.
func (s *AppService) TerminalQuickFix(command, output string, exitCode int, cwd string) map[string]any {
	lowerOut := strings.ToLower(output)
	if strings.Contains(lowerOut, "module not found") || strings.Contains(lowerOut, "cannot find module") {
		return map[string]any{
			"success":          true,
			"explanation":      "缺少依赖模块，建议运行包管理器安装对应依赖。",
			"suggestedCommand": "pnpm install",
		}
	}
	if strings.Contains(lowerOut, "go.mod file not found") || strings.Contains(lowerOut, "no required module provides") {
		return map[string]any{
			"success":          true,
			"explanation":      "Go 模块依赖未同步或缺少声明，建议运行 go mod tidy 同步依赖。",
			"suggestedCommand": "go mod tidy",
		}
	}
	if strings.Contains(lowerOut, "command not found") || strings.Contains(lowerOut, "is not recognized as an internal or external command") {
		return map[string]any{
			"success":          true,
			"explanation":      "系统环境变量 PATH 中未找到该执行命令，请检查相关工具链是否安装。",
			"suggestedCommand": "",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	fixPrompt := fmt.Sprintf("终端执行命令 `%s` 异常退出 (退出码 %d)。\n终端日志:\n```\n%s\n```\n请用简洁的一两句话说明失败原因，并给出修复建议。", command, exitCode, output)
	fixRes := s.sendMessageCore(ctx, "terminal-quickfix", fixPrompt, nil, nil)
	// A degraded reply carries a failure notice, not an explanation of the command.
	if fixRes.Error == "" && fixRes.Degraded == "" && len(fixRes.Messages) > 0 {
		for _, m := range fixRes.Messages {
			if m.Role == "assistant" && m.Content != "" {
				return map[string]any{
					"success":          true,
					"explanation":      m.Content,
					"suggestedCommand": "",
				}
			}
		}
	}

	return map[string]any{
		"success":          true,
		"explanation":      fmt.Sprintf("命令以退出码 %d 异常终止，请根据终端错误日志排查。", exitCode),
		"suggestedCommand": "",
	}
}

// GenerateCompletion generates inline code completion (Ghost Text) for OpenSumi / Monaco.
func (s *AppService) GenerateCompletion(prefix, suffix, language string) map[string]any {
	if strings.TrimSpace(prefix) == "" {
		return map[string]any{"items": []any{}}
	}

	if len(prefix) > 2000 {
		prefix = prefix[len(prefix)-2000:]
	}
	if len(suffix) > 1000 {
		suffix = suffix[:1000]
	}

	system := "You are a code completion engine (Fill In The Middle). Only output the code that should be inserted between the prefix and suffix. Do not wrap in markdown quotes. Do not repeat code from prefix or suffix."
	user := fmt.Sprintf("<PREFIX>\n%s\n<SUFFIX>\n%s\n<INSERT_POINT>", prefix, suffix)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	resp, err := ChatOnce(ctx, s.rt.LLMConfig(), system, user)
	if err != nil {
		return map[string]any{"items": []any{}}
	}

	insertText := strings.Trim(resp, "\r`")
	if insertText == "" {
		return map[string]any{"items": []any{}}
	}

	return map[string]any{
		"items": []map[string]any{
			{"insertText": insertText},
		},
	}
}

// FixProblem analyzes a diagnostic error and proposes an immediate inline edit.
func (s *AppService) FixProblem(filePath, code, errorMessage string) map[string]any {
	instruction := fmt.Sprintf("修复以下代码中的诊断报错: %s", errorMessage)
	return s.GenerateInlineEdit(filePath, "", code, "", "", instruction)
}
