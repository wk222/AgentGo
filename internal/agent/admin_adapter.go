package agent

import (
        "context"
        "fmt"
        "os"
        "strings"
)

// RunAdminTask implements admin.AgentRunner using Eino DeepAgent when checkpoint is available.
func (r *Runner) RunAdminTask(ctx context.Context, sessionID, goal string) (string, error) {
        cfg := r.adminLLMConfig()
        if cfg.APIKey == "" {
                return "", fmt.Errorf("LLM APIKey is empty")
        }
        goal = strings.TrimSpace(goal)
        if goal == "" {
                return "", fmt.Errorf("empty admin goal")
        }

        switch path, exclusive := adminExecPlan(r.cpStore != nil, os.Getenv); path {
        case adminPathPlanExecute:
                out, err := r.runAdminPlanExecute(ctx, cfg, sessionID, goal)
                if err == nil && strings.TrimSpace(out) != "" {
                        return out, nil
                }
                // In *_ONLY mode the path is exclusive: surface the failure instead of
                // silently degrading to the legacy Generate fallback.
                if exclusive {
                        if err != nil {
                                return "", fmt.Errorf("admin plan-execute failed: %w", err)
                        }
                        return "", fmt.Errorf("admin plan-execute returned empty output")
                }
        case adminPathDeep:
                out, err := r.runAdminDeep(ctx, cfg, sessionID, goal)
                if err == nil && strings.TrimSpace(out) != "" {
                        return out, nil
                }
                if exclusive {
                        if err != nil {
                                return "", fmt.Errorf("admin deep agent failed: %w", err)
                        }
                        return "", fmt.Errorf("admin deep agent returned empty output")
                }
        }

        actx := WithSessionMode(BindRunContext(ctx, sessionID), SessionMode{Profile: ModeAdmin, Canvas: CanvasDeep})
        res, err := r.Generate(actx, cfg, sessionID, goal)
        if err != nil {
                return "", err
        }
        if res != nil && res.Content != "" {
                return res.Content, nil
        }
        return "", fmt.Errorf("empty result from admin task")
}

func (r *Runner) adminLLMConfig() LLMSettings {
        if r.llmProvider != nil {
                return r.llmProvider()
        }
        return LLMSettings{}
}

// adminPath identifies which admin execution strategy RunAdminTask should attempt.
type adminPath int

const (
        adminPathLegacy adminPath = iota
        adminPathDeep
        adminPathPlanExecute
)

// adminExecPlan resolves the admin execution path (and whether it is exclusive) from
// checkpoint availability and the AGENTGO_ADMIN_* env flags. It is pure so the flag
// precedence is unit-testable without an LLM. exclusive=true means the *_ONLY flag is
// set and a failure/empty result must surface as an error rather than fall through to
// the legacy Generate path.
func adminExecPlan(hasCheckpoint bool, env func(string) string) (path adminPath, exclusive bool) {
        if !hasCheckpoint || env("AGENTGO_ADMIN_LEGACY") == "1" {
                return adminPathLegacy, false
        }
        if env("AGENTGO_ADMIN_PLANEXECUTE") == "1" {
                return adminPathPlanExecute, env("AGENTGO_ADMIN_PE_ONLY") == "1"
        }
        return adminPathDeep, env("AGENTGO_ADMIN_DEEP_ONLY") == "1"
}
