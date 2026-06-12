package bridge

import (
	"context"
	"strings"

	"agentgo/internal/agent"
)

func (s *AppService) sendMessageCore(ctx context.Context, sessionID, userText string, images []string, streamEmit func(string)) SendMessageResult {
	userText = strings.TrimSpace(userText)
	if userText == "" && len(images) == 0 {
		return SendMessageResult{Error: "消息不能为空"}
	}

	cfg := s.rt.LLMConfig()
	runner := s.rt.AgentRunner()
	if cfg.APIKey == "" || runner == nil {
		answer, err := ChatOnce(ctx, cfg, "", userText)
		if err != nil {
			answer, err = quickChatProbe(ctx, cfg, userText)
		}
		if err != nil {
			return SendMessageResult{Error: err.Error()}
		}
		_ = s.rt.Memory().Ingest(ctx, memoryRecordFromTurn(userText, answer))
		return SendMessageResult{Messages: []ChatMessageDTO{{Role: "assistant", Type: "text", Content: answer}}}
	}

	llm := s.rt.AgentLLMSettings()
	var runRes *agent.RunResult
	var err error
	if streamEmit != nil {
		runRes, err = runner.GenerateStream(ctx, llm, sessionID, userText, images, streamEmit)
	} else {
		runRes, err = runner.Generate(ctx, llm, sessionID, userText, images)
	}
	if err != nil {
		answer, fbErr := ChatOnce(ctx, cfg, "", userText)
		if fbErr != nil {
			return SendMessageResult{Error: err.Error()}
		}
		_ = s.rt.Memory().Ingest(ctx, memoryRecordFromTurn(userText, answer))
		return SendMessageResult{Messages: []ChatMessageDTO{{Role: "assistant", Type: "text", Content: answer}}}
	}

	msgs := []ChatMessageDTO{}
	if runRes.Content != "" {
		msgs = append(msgs, ChatMessageDTO{Role: "assistant", Type: "text", Content: runRes.Content})
	}
	if runRes.PendingApproval != nil {
		p := runRes.PendingApproval
		interruptID := p.InterruptID
		if interruptID == "" {
			interruptID = p.ApprovalID
		}
		approvalID := p.ApprovalID
		if approvalID == "" {
			approvalID = interruptID
		}
		s.rt.pending.Set(pendingRun{
			SessionID: sessionID, UserText: userText,
			ToolName: p.ToolName, Arguments: p.Arguments,
			ApprovalID: approvalID, InterruptID: interruptID,
		})
		if p.ToolName == "ask_user" && p.Question != nil {
			s.emitQuestion(p.Question, p.ApprovalID, sessionID)
			msgs = append(msgs, ChatMessageDTO{
				Role: "assistant", Type: "question", ApprovalID: p.ApprovalID,
				Content: p.Question.Prompt, ToolName: "ask_user", Status: "pending",
			})
		} else {
			if s.app != nil {
				payload := map[string]any{
					"approval_id": p.ApprovalID,
					"tool_name":   p.ToolName,
					"arguments":   p.Arguments,
					"prompt":      "等待审批: " + p.ToolName,
				}
				if sessionID != "" {
					payload["session_id"] = sessionID
				}
				s.app.Event.Emit("approval:pending", payload)
			}
			msgs = append(msgs, ChatMessageDTO{
				Role: "assistant", Type: "approval", ApprovalID: p.ApprovalID,
				ToolName: p.ToolName, Arguments: p.Arguments, Status: "pending",
				Content: "等待审批: " + p.ToolName,
			})
		}
	} else if runRes.Content != "" {
		_ = s.rt.Memory().Ingest(ctx, memoryRecordFromTurn(userText, runRes.Content))
	}
	if len(msgs) == 0 {
		if runRes != nil && runRes.UsedTools {
			return SendMessageResult{Messages: msgs}
		}
		msgs = append(msgs, ChatMessageDTO{Role: "assistant", Type: "text", Content: "（无回复）"})
	}
	return SendMessageResult{Messages: msgs}
}
