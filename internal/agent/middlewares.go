package agent

import (
	"context"
	"os"
	"path/filepath"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/agentsmd"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/components/model"

	"agentgo/internal/skills"
)

// BuildTypedMiddlewareStack builds a generic Eino middleware stack for either *schema.Message or *schema.AgenticMessage.
func BuildTypedMiddlewareStack[M adk.MessageType](ctx context.Context, baseModel model.BaseModel[M], workspaceRoot, dataDir string) ([]adk.TypedChatModelAgentMiddleware[M], error) {
	var stack []adk.TypedChatModelAgentMiddleware[M]

	// 1. Patch suspended tool calls
	if patchMW, err := patchtoolcalls.NewTyped[M](ctx, nil); err == nil && patchMW != nil {
		stack = append(stack, patchMW)
	}

	// 2. Summarization (Eino official MW; threshold from ExecutionCanvas)
	policy := CanvasPolicyFromContext(ctx)
	sumMW, err := summarization.NewTyped[M](ctx, &summarization.TypedConfig[M]{
		Model:   baseModel,
		Trigger: &summarization.TriggerCondition{ContextTokens: policy.SummarizationTokens},
	})
	if err != nil {
		return stack, err
	}
	stack = append(stack, sumMW)

	// 3. Agents.md injection
	if EinoAgentsMDEnabled() {
		paths := agentsMDPaths(workspaceRoot)
		if len(paths) > 0 {
			am, err := agentsmd.NewTyped[M](ctx, &agentsmd.Config{
				Backend:             newOSAgentsMDBackend(workspaceRoot),
				AgentsMDFiles:       paths,
				AllAgentsMDMaxBytes: 120_000,
			})
			if err != nil {
				return stack, err
			}
			stack = append(stack, am)
		}
	}

	// 4. Skills injection
	if EinoSkillMWEnabled() {
		sm, err := skill.NewTyped[M](ctx, &skill.TypedConfig[M]{
			Backend:    &loaderSkillBackend{loader: skills.NewLoader(workspaceRoot)},
			UseChinese: true,
		})
		if err != nil {
			return stack, err
		}
		if sm != nil {
			stack = append(stack, sm)
		}
	}

	// 5. Reduction (Clear & optional Truncation)
	redBase := BuildReductionConfig(dataDir)
	redCfg := &reduction.TypedConfig[M]{
		SkipTruncation:    redBase.SkipTruncation,
		Backend:           redBase.Backend,
		RootDir:           redBase.RootDir,
		MaxTokensForClear: int64(policy.ReductionClearTokens),
		MaxLengthForTrunc: redBase.MaxLengthForTrunc,
		ReadFileToolName:  redBase.ReadFileToolName,
	}
	if redMW, err := reduction.NewTyped[M](ctx, redCfg); err == nil {
		stack = append(stack, redMW)
	}

	return stack, nil
}

type loaderSkillBackend struct {
	loader *skills.Loader
}

func (b *loaderSkillBackend) List(ctx context.Context) ([]skill.FrontMatter, error) {
	list := b.loader.Reload()
	out := make([]skill.FrontMatter, 0, len(list))
	for _, s := range list {
		out = append(out, skill.FrontMatter{Name: s.Name, Description: s.Description})
	}
	return out, nil
}

func (b *loaderSkillBackend) Get(ctx context.Context, name string) (skill.Skill, error) {
	sk, ok := b.loader.Get("skill:" + name)
	if !ok {
		sk, ok = b.loader.Get(name)
	}
	if !ok {
		b.loader.Reload()
		sk, ok = b.loader.Get("skill:" + name)
	}
	if !ok {
		return skill.Skill{}, os.ErrNotExist
	}
	return skill.Skill{
		FrontMatter:   skill.FrontMatter{Name: sk.Name, Description: sk.Description},
		Content:       sk.Content,
		BaseDirectory: filepath.Dir(sk.Path),
	}, nil
}
