package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/callbacks"
	"github.com/google/uuid"
	"strings"

	"agentgo/internal/checkpoint"
	"agentgo/internal/event"
)

// NewEventCallbackHandler creates an Eino callbacks.Handler that feeds tool execution and error events to the channel.
func NewEventCallbackHandler(ch chan<- *event.Event, invocationID string) callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
			if info != nil {
				if info.Component == "Tools" || info.Component == "tool" {
					e := event.New(invocationID, "system",
						event.WithTag("tool_start"),
						event.WithBranch(info.Name),
					)
					e.Response = &event.Response{
						Object: "tool_start",
						Choices: []event.Choice{
							{
								Index:        0,
								FinishReason: info.Name,
							},
						},
					}
					_ = event.EmitEvent(ctx, ch, e)
				}
			}
			return ctx
		}).
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
			if info != nil {
				if info.Component == "Tools" || info.Component == "tool" {
					e := event.New(invocationID, "system",
						event.WithTag("tool_end"),
						event.WithBranch(info.Name),
					)
					e.Response = &event.Response{
						Object: "tool_end",
						Choices: []event.Choice{
							{
								Index:        0,
								FinishReason: info.Name,
							},
						},
					}
					_ = event.EmitEvent(ctx, ch, e)
				}
			}
			return ctx
		}).
		OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			if info != nil && err != nil {
				e := event.New(invocationID, "system",
					event.WithTag("error"),
					event.WithBranch(info.Name),
				)
				e.Response = &event.Response{
					Object: "error",
					Error: &event.ResponseError{
						Type:    string(info.Component),
						Message: err.Error(),
					},
				}
				_ = event.EmitEvent(ctx, ch, e)
			}
			return ctx
		}).
		Build()
}

// GenerateEventStream runs the agent and yields a structured event stream via channels.
func (r *Runner) GenerateEventStream(ctx context.Context, cfg LLMSettings, sessionID, userText string) (<-chan *event.Event, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("未配置 API Key")
	}

	eventCh := make(chan *event.Event, 100)
	invocationID := uuid.NewString()

	go func() {
		defer close(eventCh)

		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		if r.runControl != nil {
			cleanSessionID := strings.TrimPrefix(sessionID, "agentic:")
			r.runControl.SetCtxCancel(cleanSessionID, cancel)
			defer r.runControl.Clear(cleanSessionID)
		}

		var content string
		var pause *PendingApproval
		var interruptID string
		var err error

		content, pause, interruptID, err = r.runADKEventStream(runCtx, cfg, sessionID, userText, eventCh, invocationID)

		if err != nil {
			e := event.New(invocationID, "system", event.WithTag("error"))
			e.Response = &event.Response{
				Object: "error",
				Error: &event.ResponseError{
					Type:    "run_error",
					Message: err.Error(),
				},
			}
			_ = event.EmitEvent(runCtx, eventCh, e)
			return
		}

		// Emit final RunnerCompletion event
		e := event.New(invocationID, "system", event.WithTag("completion"))
		e.Response = &event.Response{
			Done:   true,
			Object: "runner_completion",
			Choices: []event.Choice{
				{
					Index:        0,
					Message:      content,
					FinishReason: "stop",
				},
			},
		}
		if pause != nil && interruptID != "" {
			e.Response.Choices[0].FinishReason = "interrupted"
		}
		_ = event.EmitEvent(runCtx, eventCh, e)
	}()

	return eventCh, nil
}

// ResumeEventStream continues an ADK checkpoint interrupt, streaming outputs via Event Channel.
func (r *Runner) ResumeEventStream(ctx context.Context, cfg LLMSettings, sessionID, interruptID string, resumeData any) (<-chan *event.Event, error) {
	if r.cpStore == nil {
		return nil, fmt.Errorf("checkpoint store unavailable")
	}

	eventCh := make(chan *event.Event, 100)
	invocationID := uuid.NewString()

	go func() {
		defer close(eventCh)

		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		if r.runControl != nil {
			cleanSessionID := strings.TrimPrefix(sessionID, "agentic:")
			r.runControl.SetCtxCancel(cleanSessionID, cancel)
			defer r.runControl.Clear(cleanSessionID)
		}

		var content string
		var pause *PendingApproval
		var id string
		var err error

		runCtx = r.withSessionMode(BindRunContext(runCtx, sessionID), sessionID)
		agentInstance, bErr := r.buildChatModelAgent(runCtx, cfg)
		if bErr != nil {
			err = bErr
		} else {
			content, pause, id, err = r.resumeADKEventStream(runCtx, cfg, sessionID, interruptID, resumeData, agentInstance, eventCh, invocationID)
		}

		if err != nil {
			e := event.New(invocationID, "system", event.WithTag("error"))
			e.Response = &event.Response{
				Object: "error",
				Error: &event.ResponseError{
					Type:    "resume_error",
					Message: err.Error(),
				},
			}
			_ = event.EmitEvent(runCtx, eventCh, e)
			return
		}

		// Emit final completion event
		e := event.New(invocationID, "system", event.WithTag("completion"))
		e.Response = &event.Response{
			Done:   true,
			Object: "runner_completion",
			Choices: []event.Choice{
				{
					Index:        0,
					Message:      content,
					FinishReason: "stop",
				},
			},
		}
		if pause != nil && id != "" {
			e.Response.Choices[0].FinishReason = "interrupted"
		}
		_ = event.EmitEvent(runCtx, eventCh, e)
	}()

	return eventCh, nil
}

func (r *Runner) runADKEventStream(ctx context.Context, cfg LLMSettings, sessionID, userText string, ch chan<- *event.Event, invocationID string) (string, *PendingApproval, string, error) {
	ctx = r.withSessionMode(BindRunContext(ctx, sessionID), sessionID)

	chatAgent, err := r.buildChatModelAgent(ctx, cfg)
	if err != nil {
		return "", nil, "", err
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           chatAgent,
		CheckPointStore: r.cpStore,
		EnableStreaming: true,
	})

	opts := r.queryOptions(ctx, sessionID)
	opts = append(opts, adk.WithCallbacks(NewEventCallbackHandler(ch, invocationID)))

	if r.runControl != nil {
		defer r.runControl.Clear(sessionID)
	}

	iter := runner.Query(ctx, userText, opts...)
	return r.drainADKEventsToChannel(ctx, iter, ch, invocationID)
}

func (r *Runner) resumeADKEventStream(ctx context.Context, cfg LLMSettings, sessionID, interruptID string, resumeData any, agentInstance adk.Agent, ch chan<- *event.Event, invocationID string) (string, *PendingApproval, string, error) {
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agentInstance, CheckPointStore: r.cpStore, EnableStreaming: true})
	opts := r.queryOptions(ctx, sessionID)
	opts = append(opts, adk.WithCallbacks(NewEventCallbackHandler(ch, invocationID)))

	if r.runControl != nil {
		defer r.runControl.Clear(sessionID)
	}

	cpID := checkpoint.CheckpointIDForSession(sessionID)
	iter, err := runner.ResumeWithParams(ctx, cpID, &adk.ResumeParams{
		Targets: map[string]any{interruptID: resumeData},
	}, opts...)
	if err != nil {
		return "", nil, "", err
	}
	return r.drainADKEventsToChannel(ctx, iter, ch, invocationID)
}

func (r *Runner) drainADKEventsToChannel(ctx context.Context, iter *adk.AsyncIterator[*adk.AgentEvent], ch chan<- *event.Event, invocationID string) (content string, pause *PendingApproval, interruptID string, err error) {
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
				pauseBytes, _ := json.Marshal(p)
				e := event.New(invocationID, "system", event.WithTag("interrupt"))
				e.Response = &event.Response{
					Object: "interrupt",
					Choices: []event.Choice{
						{
							Index:        0,
							Message:      string(pauseBytes),
							FinishReason: id,
						},
					},
				}
				_ = event.EmitEvent(ctx, ch, e)
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
					e := event.New(invocationID, "assistant", event.WithTag("chunk"))
					e.Response = &event.Response{
						Choices: []event.Choice{
							{
								Index: 0,
								Delta: chunk.Content,
							},
						},
					}
					_ = event.EmitEvent(ctx, ch, e)
				}
			}
			sr.Close()
			continue
		}
		if mv.Message != nil && mv.Message.Content != "" {
			content += mv.Message.Content
			e := event.New(invocationID, "assistant", event.WithTag("chunk"))
			e.Response = &event.Response{
				Choices: []event.Choice{
					{
						Index: 0,
						Delta: mv.Message.Content,
					},
				},
			}
			_ = event.EmitEvent(ctx, ch, e)
		}
	}
	return content, pause, interruptID, nil
}

func sendError(ctx context.Context, ch chan<- *event.Event, invocationID, errorType string, err error) {
	e := event.New(invocationID, "system", event.WithTag("error"))
	e.Response = &event.Response{
		Object: "error",
		Error: &event.ResponseError{
			Type:    errorType,
			Message: err.Error(),
		},
	}
	_ = event.EmitEvent(ctx, ch, e)
}
