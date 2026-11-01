package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/checkpoint"
)

// toolUsageTracker records whether the current run actually invoked tools.
// RunResult.UsedTools must come from observed events: hardcoding it made
// refusal-recovery (armor break) unreachable in real runs, because its gate
// requires a turn with no tool use.
//
// outputs remembers which tool-failure phrases real tool outputs of this run
// contained, so the audit-only claim check (claim_check.go) can tell a quoted
// error that happened from one that did not.
type toolUsageTracker struct {
	used    atomic.Bool
	outputs toolOutputPhrases
}

type toolUsageCtxKey struct{}

func withToolUsageTracker(ctx context.Context) (context.Context, *toolUsageTracker) {
	if ctx == nil {
		ctx = context.Background()
	}
	if tr, ok := ctx.Value(toolUsageCtxKey{}).(*toolUsageTracker); ok && tr != nil {
		return ctx, tr
	}
	tr := &toolUsageTracker{}
	return context.WithValue(ctx, toolUsageCtxKey{}, tr), tr
}

func markToolUsed(ctx context.Context) {
	if tr, ok := ctx.Value(toolUsageCtxKey{}).(*toolUsageTracker); ok && tr != nil {
		tr.used.Store(true)
	}
}

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
	opts = append(opts, adk.WithCallbacks(r.runUsageHandler(runMeterFrom(ctx))))
	if CallTraceEnabled() {
		opts = append(opts, adk.WithCallbacks(newUsageSinkHandler(func(modelName string, tu *model.TokenUsage) {
			recordCallTrace(ctx, "", "", TraceStageUsage, 0, map[string]any{
				"model": modelName, "prompt_tokens": tu.PromptTokens, "cached_tokens": tu.PromptTokenDetails.CachedTokens,
				"completion_tokens": tu.CompletionTokens, "reasoning_tokens": tu.CompletionTokensDetails.ReasoningTokens,
			})
		}, nil)))
	}
	if obs := usageObserverFrom(ctx); obs != nil {
		opts = append(opts, adk.WithCallbacks(newUsageCallbackHandler(obs)))
	}
	return opts
}

// drainADKEvents runs Query/Resume and collects assistant text + interrupt metadata.
func (r *Runner) drainADKEvents(ctx context.Context, iter *adk.AsyncIterator[*adk.AgentEvent], emit func(string)) (string, *PendingApproval, string, error) {
	return r.drainADKEventsWithCallbacks(ctx, iter, func(text string) error {
		if emit != nil {
			emit(text)
		}
		return nil
	}, nil)
}

// Both desktop text and structured channels consume the same protocol stream.
// Only io.EOF completes a model stream successfully; partial output is retained
// alongside transport/cancellation errors and is never reported as a full answer.
func (r *Runner) drainADKEventsWithCallbacks(ctx context.Context, iter *adk.AsyncIterator[*adk.AgentEvent], emit func(string) error, interrupted func(string, *PendingApproval) error) (content string, pause *PendingApproval, interruptID string, err error) {
	act := runActivityFrom(ctx)
	r.runControl.track(SessionIDFromContext(ctx), act)
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		act.touch()
		if ev.Err != nil {
			if willRetry(ev.Err) {
				continue // the model call is being repeated; the next event carries the new attempt
			}
			if hitIterationLimit(ev.Err) {
				content += iterationLimitNotice
				if emit != nil {
					if e := emit(iterationLimitNotice); e != nil {
						return content, nil, "", e
					}
				}
				return content, nil, "", nil
			}
			return content, nil, "", ev.Err
		}
		if ev.Action != nil && ev.Action.Interrupted != nil {
			id, p := pickInterruptPause(ev.Action.Interrupted.InterruptContexts)
			if p != nil {
				interruptID, pause = id, p
				if interrupted != nil {
					if e := interrupted(id, p); e != nil {
						return content, pause, interruptID, e
					}
				}
			}
		}
		if ev.Output == nil || ev.Output.MessageOutput == nil {
			continue
		}
		mv := ev.Output.MessageOutput
		if (mv.Message != nil && len(mv.Message.ToolCalls) > 0) || mv.Role == schema.Tool ||
			(mv.Message != nil && mv.Message.Role == schema.Tool) {
			markToolUsed(ctx)
		}
		// Tool results are not assistant prose: keep them out of the returned content and the
		// streamed deltas, otherwise raw tool JSON gets prepended to the final answer.
		if mv.Role == schema.Tool || (mv.Message != nil && mv.Message.Role == schema.Tool) {
			noteToolOutput(ctx, toolMessageText(mv))
			continue
		}
		ro := reasoningObserverFrom(ctx)
		extractReasoning := func(rc string, extra map[string]any) string {
			if rc != "" {
				return rc
			}
			if extra != nil {
				if v, ok := extra["reasoning-content"].(string); ok && v != "" {
					return v
				}
				if v, ok := extra["_eino_deepseek_reasoning_content"].(string); ok && v != "" {
					return v
				}
			}
			return ""
		}

		if mv.IsStreaming && mv.MessageStream != nil {
			sr := mv.MessageStream
			attemptStart := len(content)
			for {
				chunk, rerr := sr.Recv()
				if rerr != nil {
					if willRetry(rerr) {
						// This attempt is being thrown away and re-run: keep its text out
						// of the answer (what was already streamed to the screen stays).
						content = content[:attemptStart]
						break
					}
					if !errors.Is(rerr, io.EOF) {
						sr.Close()
						return content, pause, interruptID, rerr
					}
					break
				}
				act.touch()
				if chunk != nil {
					if len(chunk.ToolCalls) > 0 {
						markToolUsed(ctx)
					}
					if rc := extractReasoning(chunk.ReasoningContent, chunk.Extra); rc != "" && ro != nil {
						ro.ReasoningDelta(rc)
					}
					if chunk.Content != "" {
						content += chunk.Content
						if emit != nil {
							if e := emit(chunk.Content); e != nil {
								sr.Close()
								return content, pause, interruptID, e
							}
						}
					}
				}
			}
			sr.Close()
			continue
		}
		if mv.Message != nil {
			if rc := extractReasoning(mv.Message.ReasoningContent, mv.Message.Extra); rc != "" && ro != nil {
				ro.ReasoningDelta(rc)
			}
			if mv.Message.Content != "" {
				content += mv.Message.Content
				if emit != nil {
					if e := emit(mv.Message.Content); e != nil {
						return content, pause, interruptID, e
					}
				}
			}
		}
	}
	return content, pause, interruptID, ctx.Err()
}

func (r *Runner) runADK(ctx context.Context, cfg LLMSettings, sessionID, userText string, images []string, emit func(string), extraOpts ...adk.AgentRunOption) (*RunResult, error) {
	ctx, toolUse := withToolUsageTracker(ctx)
	ctx = withLLMBudget(ctx, cfg)
	ctx = r.withSessionMode(BindRunContext(ctx, sessionID), sessionID)

	chatAgent, err := r.buildChatModelAgent(ctx, cfg)
	if err != nil {
		return nil, err
	}

	runnerCfg, err := r.sessionRunnerConfig(ctx, sessionID, chatAgent, emit != nil, true)
	if err != nil {
		return nil, err
	}
	runner := adk.NewRunner(ctx, runnerCfg)

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

	opts = append(opts, extraOpts...)

	if r.runControl != nil {
		defer r.runControl.Clear(sessionID)
	}

	// Run, not Query: Query is Run with a single user message, and a frontend
	// may have supplied earlier turns (see WithHistory).
	iter := runner.Run(ctx, r.sessionRunMessages(ctx, sessionID, userText), opts...)
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

	r.auditUngroundedToolFailureClaims(ctx, sessionID, content, toolUse)
	return &RunResult{Content: content, UsedTools: toolUse.used.Load()}, nil
}

// ResumeInterrupt continues an ADK checkpoint interrupt (Eino-native resume).
func (r *Runner) ResumeInterrupt(ctx context.Context, cfg LLMSettings, sessionID, interruptID string, resumeData any) (*RunResult, error) {
	ctx = withLLMBudget(ctx, cfg)
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
	return r.resumeADKAgent(runCtx, cfg, sessionID, interruptID, resumeData, agent, textEmitterFrom(ctx))
}

func (r *Runner) resumeADKAgent(ctx context.Context, cfg LLMSettings, sessionID, interruptID string, resumeData any, agent adk.Agent, emit func(string)) (*RunResult, error) {
	ctx, toolUse := withToolUsageTracker(ctx)
	ctx = withLLMBudget(ctx, cfg)
	runnerCfg, configErr := r.sessionRunnerConfig(ctx, sessionID, agent, emit != nil, false)
	if configErr != nil {
		return nil, configErr
	}
	runner := adk.NewRunner(ctx, runnerCfg)
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
	r.auditUngroundedToolFailureClaims(ctx, sessionID, content, toolUse)
	return &RunResult{Content: content, UsedTools: toolUse.used.Load()}, nil
}
