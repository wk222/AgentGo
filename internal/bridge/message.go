package bridge

import (
	"context"
	"errors"
	"log"
	"strings"

	"agentgo/internal/agent"
)

const degradedReasonMax = 200

// degradedReply wraps a tool-less fallback answer so it cannot pass for the
// result of the agent run: the text says what happened, and Degraded carries
// the reason for programmatic callers. The run may already have executed some
// steps before it failed, so the notice does not claim nothing happened.
func degradedReply(cause error, answer string) SendMessageResult {
	reason := strings.Join(strings.Fields(cause.Error()), " ")
	if r := []rune(reason); len(r) > degradedReasonMax {
		reason = string(r[:degradedReasonMax]) + "…"
	}
	notice := "[能力降级] 智能体运行失败（" + reason + "），以下回复由不带工具的纯聊天生成：没有调用任何工具，不代表任务已完成；失败前的步骤可能已部分执行。"
	return SendMessageResult{
		Messages: []ChatMessageDTO{{Role: "assistant", Type: "text", Content: notice + "\n\n" + answer}},
		Degraded: reason,
	}
}

func (s *AppService) sendMessageCore(ctx context.Context, sessionID, userText string, images []string, streamEmit func(string)) SendMessageResult {
	res := s.rt.sendMessageCore(ctx, sessionID, userText, images, streamEmit)
	s.emitPendingMessages(sessionID, res.Messages)
	return res
}

func (rt *Runtime) sendMessageCore(ctx context.Context, sessionID, userText string, images []string, streamEmit func(string)) SendMessageResult {
	if err := ctx.Err(); err != nil {
		return SendMessageResult{Error: err.Error()}
	}
	userText = strings.TrimSpace(userText)
	if userText == "" && len(images) == 0 {
		return SendMessageResult{Error: "消息不能为空"}
	}

	modelInputText, refused := rt.preparePrompt(ctx, sessionID, userText)
	if refused != nil {
		return *refused
	}
	if err := ctx.Err(); err != nil {
		return SendMessageResult{Error: err.Error()}
	}
	res := rt.runMessage(ctx, sessionID, userText, modelInputText, images, streamEmit)
	if err := ctx.Err(); err != nil {
		return SendMessageResult{Error: err.Error()}
	}
	if reply, ended := turnEnd(res); ended {
		rt.fireStopHook(sessionID, reply)
	}
	return res
}

// runMessage sends one prepared message to the model and returns what came back.
func (rt *Runtime) runMessage(ctx context.Context, sessionID, userText, modelInputText string, images []string, streamEmit func(string)) SendMessageResult {
	cfg := rt.LLMConfig()
	runner := rt.AgentRunner()
	if cfg.APIKey == "" || runner == nil {
		answer, err := ChatOnce(ctx, cfg, "", modelInputText)
		if err != nil && ctx.Err() == nil {
			answer, err = quickChatProbe(ctx, cfg, modelInputText)
		}
		if err != nil {
			return SendMessageResult{Error: err.Error()}
		}
		rt.ingestTurn(ctx, userText, answer)
		return SendMessageResult{Messages: []ChatMessageDTO{{Role: "assistant", Type: "text", Content: answer}}}
	}

	llm := rt.AgentLLMSettings()
	var runRes *agent.RunResult
	var err error
	if streamEmit != nil {
		runRes, err = runner.GenerateStream(ctx, llm, sessionID, modelInputText, images, streamEmit)
	} else {
		runRes, err = runner.Generate(ctx, llm, sessionID, modelInputText, images)
	}
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return SendMessageResult{Error: err.Error()}
		}
		// The fallback below answers WITHOUT tools; never hide why the agent run failed.
		log.Printf("[chat] agent run failed (session=%s), falling back to tool-less chat: %v", sessionID, err)
		agent.DebugLogf("agent run failed (session=%s), falling back to tool-less chat: %v", sessionID, err)
		answer, fbErr := ChatOnce(ctx, cfg, "", modelInputText)
		if fbErr != nil {
			return SendMessageResult{Error: err.Error()}
		}
		res := degradedReply(err, answer)
		rt.ingestTurn(ctx, userText, res.Messages[0].Content)
		return res
	}
	if runRes == nil {
		return SendMessageResult{Error: "智能体运行没有返回结果"}
	}

	msgs := []ChatMessageDTO{}
	if runRes.Content != "" {
		msgs = append(msgs, ChatMessageDTO{Role: "assistant", Type: "text", Content: runRes.Content})
	}
	if runRes.PendingApproval != nil {
		msgs = append(msgs, rt.registerPending(sessionID, userText, runRes.PendingApproval))
	} else if runRes.Content != "" {
		rt.ingestTurn(ctx, userText, runRes.Content)
	}
	if len(msgs) == 0 {
		if runRes != nil && runRes.UsedTools {
			return SendMessageResult{Messages: msgs}
		}
		msgs = append(msgs, ChatMessageDTO{Role: "assistant", Type: "text", Content: "（无回复）"})
	}
	return SendMessageResult{Messages: msgs}
}

func (rt *Runtime) ingestTurn(ctx context.Context, userText, answer string) {
	if mem := rt.Memory(); mem != nil && ctx.Err() == nil {
		_ = mem.Ingest(ctx, memoryRecordFromTurn(userText, answer))
	}
}
