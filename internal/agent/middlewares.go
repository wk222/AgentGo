package agent

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/agentsmd"
	backgroundtaskmw "github.com/cloudwego/eino/adk/middlewares/backgroundtask"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/adk/middlewares/plantask"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/config"
	"agentgo/internal/skills"
)

// EinoBackgroundTaskEnabled checks if backgroundtask middleware is enabled (default true).
func EinoBackgroundTaskEnabled() bool {
	return config.EinoBGTask.On()
}

// EinoPlanTaskEnabled checks if plantask middleware is enabled (default true).
func EinoPlanTaskEnabled() bool {
	return config.EinoPlanTask.On()
}

// EinoSpillEnabled checks if spill offloading middleware is enabled (default true).
func EinoSpillEnabled() bool {
	return config.EinoSpill.On()
}

// BuildTypedMiddlewareStack builds a generic Eino middleware stack for either *schema.Message or *schema.AgenticMessage.
func BuildTypedMiddlewareStack[M adk.MessageType](ctx context.Context, baseModel model.BaseModel[M], workspaceRoot, dataDir string, bgCoord *BackgroundTaskCoordinator) ([]adk.TypedChatModelAgentMiddleware[M], error) {
	var stack []adk.TypedChatModelAgentMiddleware[M]
	stack = append(stack, &evidenceMiddleware[M]{})
	policy := CanvasPolicyFromContext(ctx)
	var spMW *SpillMiddleware
	if EinoSpillEnabled() {
		if _, ok := any(new(M)).(**schema.Message); ok {
			if store, err := BuildDefaultSpillStore(dataDir); err == nil {
				spMW, _ = NewSpillMiddleware(store)
			}
		}
	}

	// 1. Patch suspended tool calls
	if patchMW, err := patchtoolcalls.NewTyped[M](ctx, &patchtoolcalls.Config{RemoveOrphanResults: true, RemoveDuplicateResults: true, MarkSynthetic: true, PatchedToolResultGenerator: func(_ context.Context, _ string, _ string, _ *schema.ToolArgument) (*patchtoolcalls.PatchedToolResult, error) {
		return &patchtoolcalls.PatchedToolResult{Content: "[Tool result unavailable] Execution and side effects are unknown. Verify actual state before retrying; do not treat a missing result as success."}, nil
	}}); err == nil && patchMW != nil {
		stack = append(stack, patchMW)
	}

	// Project structured source metadata before summarization can spend an
	// extra model call on its expanded representation. Generic spill stays below.
	if spMW != nil {
		if typed, ok := any(&documentViewMiddleware{spill: spMW}).(adk.TypedChatModelAgentMiddleware[M]); ok {
			stack = append(stack, typed)
		}
	}

	// 2. Summarization (Eino official MW; threshold from ExecutionCanvas)
	if baseModel != nil {
		sumMW, err := summarization.NewTyped[M](ctx, contextSummaryConfig(baseModel, policy))
		if err != nil {
			return stack, err
		}
		stack = append(stack, &resilientSummary[M]{TypedChatModelAgentMiddleware: sumMW, timeout: 20 * time.Second})
	}

	// 3. Agents.md injection
	if EinoAgentsMDEnabled() {
		instructions := discoverInstructions(workspaceRoot, dataDir)
		if len(instructions.Paths) > 0 {
			am, err := agentsmd.NewTyped[M](ctx, &agentsmd.Config{
				Backend:             newInstructionBackend(workspaceRoot, instructions),
				AgentsMDFiles:       instructions.Paths,
				AllAgentsMDMaxBytes: 120_000,
				PerAgentsMDMaxBytes: 32_000,
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

	// 5. Durable Background Task Control Middleware (task_output, task_stop)
	if EinoBackgroundTaskEnabled() && bgCoord != nil && bgCoord.Manager() != nil {
		bgCfg := &backgroundtaskmw.TypedConfig[M]{
			Manager:                      bgCoord.Manager(),
			ProgressReadersByExecutorKey: bgCoord.ProgressReaders(),
		}
		if bgMW, err := backgroundtaskmw.NewTyped[M](ctx, bgCfg); err == nil && bgMW != nil {
			stack = append(stack, bgMW)
		}
	}

	// 6. PlanTask Middleware (TaskCreate, TaskGet, TaskUpdate, TaskList)
	if EinoPlanTaskEnabled() && policy.PlanTasks {
		taskDir := planTaskDirectory(ctx, workspaceRoot, dataDir)
		_ = os.MkdirAll(taskDir, 0o755)
		ptMW, err := plantask.NewTyped[M](ctx, &plantask.Config{
			Backend: newDiskPlantaskBackend(taskDir),
			BaseDir: taskDir,
		})
		if err == nil && ptMW != nil {
			stack = append(stack, ptMW)
		}
	}

	// 7. Spill Middleware (large tool output offloading with locator preview)
	if spMW != nil {
		if typedSpill, ok := any(spMW).(adk.TypedChatModelAgentMiddleware[M]); ok {
			stack = append(stack, typedSpill)
		}
	}

	// 8. Reduction (Clear & optional Truncation)
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
