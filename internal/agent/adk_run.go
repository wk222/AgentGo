package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/checkpoint"
)

func (r *Runner) queryOptions(ctx context.Context, sessionID string) []adk.AgentRunOption {
	cpID := checkpoint.CheckpointIDForSession(sessionID)
	cancelOpt, cancelFn := adk.WithCancel()
	if r.runControl != nil {
		cleanSessionID := strings.TrimPrefix(sessionID, "agentic:")
		r.runControl.Set(cleanSessionID, cancelFn)
	}
	opts := []adk.AgentRunOption{adk.WithCheckPointID(cpID), cancelOpt}
	if _, ok := ctx.Value(traceEmitKey{}).(func(TraceRecord)); ok {
		opts = append(opts, adk.WithCallbacks(NewTraceCallbackHandler(r.capBus)))
	}
	return opts
}

// drainADKEvents runs Query/Resume and collects assistant text + interrupt metadata.
func (r *Runner) drainADKEvents(ctx context.Context, iter *adk.AsyncIterator[*adk.AgentEvent], emit func(string)) (content string, pause *PendingApproval, interruptID string, err error) {
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			return "", nil, "", ev.Err
		}
		if ev.Action != nil && ev.Action.Interrupted != nil {
			id, p := pickInterruptPause(ev.Action.Interrupted.InterruptContexts)
			if p != nil {
				interruptID, pause = id, p
			}
		}
		if ev.Output == nil || ev.Output.MessageOutput == nil {
			continue
		}
		mv := ev.Output.MessageOutput
		if mv.IsStreaming && mv.MessageStream != nil {
			sr := mv.MessageStream
			for {
				chunk, rerr := sr.Recv()
				if rerr != nil {
					break
				}
				if chunk != nil && chunk.Content != "" {
					content += chunk.Content
					if emit != nil {
						emit(chunk.Content)
					}
				}
			}
			sr.Close()
			continue
		}
		if mv.Message != nil && mv.Message.Content != "" {
			content += mv.Message.Content
			if emit != nil {
				emit(mv.Message.Content)
			}
		}
	}
	return content, pause, interruptID, nil
}

func (r *Runner) runADK(ctx context.Context, cfg LLMSettings, sessionID, userText string, images []string, emit func(string)) (*RunResult, error) {
	ctx = r.withSessionMode(BindRunContext(ctx, sessionID), sessionID)

	chatAgent, err := r.buildChatModelAgent(ctx, cfg)
	if err != nil {
		return nil, err
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           chatAgent,
		CheckPointStore: r.cpStore,
		EnableStreaming: emit != nil,
	})

	opts := r.queryOptions(ctx, sessionID)
	if len(images) > 0 {
		opts = append(opts, adk.WithHistoryModifier(func(ctx context.Context, msgs []adk.Message) []adk.Message {
			if len(msgs) > 0 {
				last := msgs[len(msgs)-1]
				if last.Role == schema.User {
					for _, imgBase64 := range images {
						b64 := imgBase64
						last.UserInputMultiContent = append(last.UserInputMultiContent, schema.MessageInputPart{
							Type: schema.ChatMessagePartTypeImageURL,
							Image: &schema.MessageInputImage{
								MessagePartCommon: schema.MessagePartCommon{
									Base64Data: &b64,
									MIMEType:   "image/jpeg",
								},
							},
						})
					}
				}
			}
			return msgs
		}))
	}

	if r.runControl != nil {
		defer r.runControl.Clear(sessionID)
	}

	iter := runner.Query(ctx, userText, opts...)
	content, pause, interruptID, err := r.drainADKEvents(ctx, iter, emit)
	if err != nil {
		return nil, err
	}

	if pause != nil && interruptID != "" {
		if pause.InterruptID == "" {
			pause.InterruptID = interruptID
		}
		if pause.ApprovalID == "" {
			pause.ApprovalID = interruptID
		}
		return &RunResult{Content: content, PendingApproval: pause, UsedTools: true}, nil
	}

	return &RunResult{Content: content, UsedTools: true}, nil
}

// ResumeInterrupt continues an ADK checkpoint interrupt (Eino-native resume).
func (r *Runner) ResumeInterrupt(ctx context.Context, cfg LLMSettings, sessionID, interruptID string, resumeData any) (*RunResult, error) {
	if r.cpStore == nil {
		return nil, fmt.Errorf("checkpoint store unavailable")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if r.runControl != nil {
		cleanSessionID := strings.TrimPrefix(sessionID, "agentic:")
		r.runControl.SetCtxCancel(cleanSessionID, cancel)
		defer r.runControl.Clear(cleanSessionID)
	}

	runCtx = r.withSessionMode(BindRunContext(runCtx, sessionID), sessionID)
	agent, err := r.buildChatModelAgent(runCtx, cfg)
	if err != nil {
		return nil, err
	}
	return r.resumeADKAgent(runCtx, cfg, sessionID, interruptID, resumeData, agent, nil)
}

func (r *Runner) resumeADKAgent(ctx context.Context, cfg LLMSettings, sessionID, interruptID string, resumeData any, agent adk.Agent, emit func(string)) (*RunResult, error) {
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, CheckPointStore: r.cpStore, EnableStreaming: emit != nil})
	opts := r.queryOptions(ctx, sessionID)
	if r.runControl != nil {
		defer r.runControl.Clear(sessionID)
	}
	cpID := checkpoint.CheckpointIDForSession(sessionID)
	iter, err := runner.ResumeWithParams(ctx, cpID, &adk.ResumeParams{
		Targets: map[string]any{interruptID: resumeData},
	}, opts...)
	if err != nil {
		return nil, err
	}
	content, pause, id, err := r.drainADKEvents(ctx, iter, emit)
	if err != nil {
		return nil, err
	}
	if pause != nil && id != "" {
		if pause.InterruptID == "" {
			pause.InterruptID = id
		}
		if pause.ApprovalID == "" {
			pause.ApprovalID = id
		}
		return &RunResult{Content: content, PendingApproval: pause, UsedTools: true}, nil
	}
	return &RunResult{Content: content, UsedTools: true}, nil
}
