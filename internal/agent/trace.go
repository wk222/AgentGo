package agent

import (
	"context"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"

	"agentgo/internal/capability"
)

// TraceRecord is one ADK callback observation for the UI.
type TraceRecord struct {
	Phase     string `json:"phase"` // start, end, error
	Component string `json:"component"`
	Name      string `json:"name"`
	Detail    string `json:"detail,omitempty"`
	At        int64  `json:"at"`
}

type traceEmitKey struct{}
type traceStartTimeKey struct{}

// WithTraceEmitter attaches a sink for ADK callback trace (Ch.06).
func WithTraceEmitter(ctx context.Context, emit func(TraceRecord)) context.Context {
	if emit == nil {
		return ctx
	}
	return context.WithValue(ctx, traceEmitKey{}, emit)
}

func traceEmitFromContext(ctx context.Context, rec TraceRecord) {
	if v, ok := ctx.Value(traceEmitKey{}).(func(TraceRecord)); ok && v != nil {
		rec.At = time.Now().UnixMilli()
		v(rec)
	}
}

// EmitTrace sends a trace record when WithTraceEmitter is on ctx (Wails agent:trace).
func EmitTrace(ctx context.Context, phase, component, name, detail string) {
	traceEmitFromContext(ctx, TraceRecord{
		Phase: phase, Component: component, Name: name, Detail: detail,
	})
}

// NewTraceCallbackHandler builds Eino callbacks.Handler for Runner.Query (aligned with Ch.06).
// It also records metrics to the CapabilityBus.
func NewTraceCallbackHandler(capBus *capability.Bus) callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
			if info != nil {
				traceEmitFromContext(ctx, TraceRecord{
					Phase: "start", Component: string(info.Component), Name: info.Name,
				})
			}
			return context.WithValue(ctx, traceStartTimeKey{}, time.Now())
		}).
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
			if info != nil {
				traceEmitFromContext(ctx, TraceRecord{
					Phase: "end", Component: string(info.Component), Name: info.Name,
				})

				// Record metrics to CapabilityBus
				if capBus != nil {
					durationMs := 0.0
					if startTime, ok := ctx.Value(traceStartTimeKey{}).(time.Time); ok {
						durationMs = float64(time.Since(startTime).Milliseconds())
					}
					
					tokens := 0
					compStr := string(info.Component)
					if compStr == "ChatModel" || compStr == "chat_model" {
						if modelOut := model.ConvCallbackOutput(output); modelOut != nil && modelOut.TokenUsage != nil {
							tokens = modelOut.TokenUsage.TotalTokens
							if tokens == 0 {
								tokens = modelOut.TokenUsage.PromptTokens + modelOut.TokenUsage.CompletionTokens
							}
						}
					}
					capBus.RecordMetric(compStr, info.Name, durationMs, tokens)
				}
			}
			return ctx
		}).
		OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			detail := ""
			if err != nil {
				detail = err.Error()
			}
			name, comp := "", ""
			if info != nil {
				name, comp = info.Name, string(info.Component)
			}
			traceEmitFromContext(ctx, TraceRecord{
				Phase: "error", Component: comp, Name: name, Detail: detail,
			})
			return ctx
		}).
		Build()
}

// TraceEmitterFromWails returns a bridge-friendly trace sink.
func TraceEmitterFromWails(emit func(map[string]any)) func(TraceRecord) {
	if emit == nil {
		return nil
	}
	return func(r TraceRecord) {
		emit(map[string]any{
			"phase": r.Phase, "component": r.Component, "name": r.Name,
			"detail": r.Detail, "at": r.At,
		})
	}
}
