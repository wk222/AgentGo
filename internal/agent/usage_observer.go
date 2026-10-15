package agent

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// UsageObserver receives token usage of each chat-model call of one run.
// Prompt tokens of the latest call approximate the current context size.
type UsageObserver interface {
	ModelUsage(prompt, completion int)
}

type usageObserverKey struct{}

// WithUsageObserver binds obs to the run carried by ctx; queryOptions then
// attaches the usage callback to the ADK run.
func WithUsageObserver(ctx context.Context, obs UsageObserver) context.Context {
	if obs == nil {
		return ctx
	}
	return context.WithValue(ctx, usageObserverKey{}, obs)
}

func usageObserverFrom(ctx context.Context) UsageObserver {
	obs, _ := ctx.Value(usageObserverKey{}).(UsageObserver)
	return obs
}

func usageModelName(mo *model.CallbackOutput) string {
	if mo == nil || mo.Config == nil {
		return ""
	}
	return mo.Config.Model
}

func isChatModel(info *callbacks.RunInfo) bool {
	return info != nil && (string(info.Component) == "ChatModel" || string(info.Component) == "chat_model")
}

// newUsageCallbackHandler reports chat-model token usage to obs. Streamed
// calls (the normal path) never reach OnEnd; their usage rides on the last
// chunk(s) of the stream copy handed to OnEndWithStreamOutput.
func newUsageCallbackHandler(obs UsageObserver) callbacks.Handler {
	return newUsageSinkHandler(func(_ string, tu *model.TokenUsage) {
		obs.ModelUsage(tu.PromptTokens, tu.CompletionTokens)
	}, nil)
}

// newUsageSinkHandler hands the full TokenUsage (cache/reasoning included) and the
// model name of every chat-model call to sink. pending, when non-nil, is held
// from the moment a streamed call starts until its usage was delivered, so the
// caller can wait for the last call before reading totals.
func newUsageSinkHandler(sink func(modelName string, tu *model.TokenUsage), pending *sync.WaitGroup) callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, out callbacks.CallbackOutput) context.Context {
			if isChatModel(info) {
				if mo := model.ConvCallbackOutput(out); mo != nil && mo.TokenUsage != nil {
					sink(usageModelName(mo), mo.TokenUsage)
				}
			}
			return ctx
		}).
		OnEndWithStreamOutputFn(func(ctx context.Context, info *callbacks.RunInfo, out *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
			if !isChatModel(info) {
				out.Close()
				return ctx
			}
			if pending != nil {
				pending.Add(1)
			}
			go func() {
				if pending != nil {
					defer pending.Done()
				}
				defer out.Close()
				var last *model.TokenUsage
				name := ""
				for {
					chunk, err := out.Recv()
					if err != nil {
						break
					}
					if mo := model.ConvCallbackOutput(chunk); mo != nil {
						if n := usageModelName(mo); n != "" {
							name = n
						}
						if mo.TokenUsage != nil {
							last = mo.TokenUsage
						}
					}
				}
				if last != nil {
					sink(name, last)
				}
			}()
			return ctx
		}).
		Build()
}
