package bridge

import (
	"agentgo/internal/governance"
	"agentgo/internal/skills"
	"agentgo/internal/tools"
	"agentgo/internal/workspace"
)

func (r *Runtime) WorkspaceInfo() WorkspaceInfo {
	if r == nil {
		return WorkspaceInfo{}
	}
	return buildWorkspaceInfo(r.WorkspaceRoot())
}

func (r *Runtime) SetWorkspaceRoot(path string) (WorkspaceInfo, error) {
	abs, err := normalizeWorkspaceRoot(path)
	if err != nil {
		return WorkspaceInfo{}, err
	}

	wsMW := workspace.NewContextMiddleware(abs)
	loader := skills.NewLoader(abs)
	loader.Reload()

	r.mu.Lock()
	r.workspace = abs
	r.wsMiddleware = wsMW
	r.skillLoader = loader
	policy := governance.BuildPolicy(r.governanceCfg.ControlMode, abs)
	if r.toolReg != nil {
		if err := tools.RegisterWorkspaceBoundTools(r.toolReg, abs); err != nil {
			r.mu.Unlock()
			return WorkspaceInfo{}, err
		}
		_ = tools.RegisterActivateSkill(r.toolReg, loader)
	}
	if r.agentRunner != nil {
		r.agentRunner.SetWorkspaceRoot(abs, wsMW, policy)
	}
	err = saveAppConfig(r.dataDir, r.llm, r.governanceCfg, WorkspaceConfig{Root: abs})
	r.mu.Unlock()
	if err != nil {
		return WorkspaceInfo{}, err
	}
	return buildWorkspaceInfo(abs), nil
}
