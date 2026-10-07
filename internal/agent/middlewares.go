package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"

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

	"agentgo/internal/skills"
)

// EinoBackgroundTaskEnabled checks if backgroundtask middleware is enabled (default true).
func EinoBackgroundTaskEnabled() bool {
	v := strings.TrimSpace(os.Getenv("AGENTGO_EINO_BG_TASK"))
	return v != "0" && strings.ToLower(v) != "false" && strings.ToLower(v) != "off"
}

// EinoPlanTaskEnabled checks if plantask middleware is enabled (default true).
func EinoPlanTaskEnabled() bool {
	v := strings.TrimSpace(os.Getenv("AGENTGO_EINO_PLAN_TASK"))
	return v != "0" && strings.ToLower(v) != "false" && strings.ToLower(v) != "off"
}

// EinoSpillEnabled checks if spill offloading middleware is enabled (default true).
func EinoSpillEnabled() bool {
	v := strings.TrimSpace(os.Getenv("AGENTGO_EINO_SPILL"))
	return v != "0" && strings.ToLower(v) != "false" && strings.ToLower(v) != "off"
}

// EinoToolResultPrunerEnabled checks if tool result pruner is enabled (default true).
func EinoToolResultPrunerEnabled() bool {
	v := strings.TrimSpace(os.Getenv("AGENTGO_EINO_PRUNER"))
	return v != "0" && strings.ToLower(v) != "false" && strings.ToLower(v) != "off"
}

// BuildTypedMiddlewareStack builds a generic Eino middleware stack for either *schema.Message or *schema.AgenticMessage.
func BuildTypedMiddlewareStack[M adk.MessageType](ctx context.Context, baseModel model.BaseModel[M], workspaceRoot, dataDir string, bgCoord *BackgroundTaskCoordinator) ([]adk.TypedChatModelAgentMiddleware[M], error) {
	var stack []adk.TypedChatModelAgentMiddleware[M]
	policy := CanvasPolicyFromContext(ctx)

	// 1. Patch suspended tool calls
	if patchMW, err := patchtoolcalls.NewTyped[M](ctx, nil); err == nil && patchMW != nil {
		stack = append(stack, patchMW)
	}

	// 2. Summarization (Eino official MW; threshold from ExecutionCanvas)
	if baseModel != nil {
		sumMW, err := summarization.NewTyped[M](ctx, &summarization.TypedConfig[M]{
			Model:   baseModel,
			Trigger: &summarization.TriggerCondition{ContextTokens: policy.SummarizationTokens},
		})
		if err != nil {
			return stack, err
		}
		stack = append(stack, sumMW)
	}

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
	if EinoPlanTaskEnabled() {
		taskDir := filepath.Join(workspaceRoot, ".tasks")
		if strings.TrimSpace(workspaceRoot) == "" {
			taskDir = filepath.Join(dataDir, "tasks")
		}
		_ = os.MkdirAll(taskDir, 0o755)
		ptMW, err := plantask.NewTyped[M](ctx, &plantask.Config{
			Backend: newDiskPlantaskBackend(taskDir),
			BaseDir: taskDir,
		})
		if err == nil && ptMW != nil {
			stack = append(stack, ptMW)
		}
	}

	// 7. Tool Result Pruner (static rule-based historical tool compaction)
	if EinoToolResultPrunerEnabled() {
		if _, ok := any(new(M)).(**schema.Message); ok {
			prunerMW := NewToolResultPrunerMiddleware(2)
			if typedPruner, ok := any(prunerMW).(adk.TypedChatModelAgentMiddleware[M]); ok {
				stack = append(stack, typedPruner)
			}
		}
	}

	// 8. Spill Middleware (large tool output offloading with locator preview)
	if EinoSpillEnabled() {
		if _, ok := any(new(M)).(**schema.Message); ok {
			if spStore, err := BuildDefaultSpillStore(dataDir); err == nil && spStore != nil {
				if spMW, err := NewSpillMiddleware(spStore); err == nil && spMW != nil {
					if typedSpill, ok := any(spMW).(adk.TypedChatModelAgentMiddleware[M]); ok {
						stack = append(stack, typedSpill)
					}
				}
			}
		}
	}

	// 9. Reduction (Clear & optional Truncation)
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
