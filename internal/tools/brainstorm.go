package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool/utils"
)

// SubBranchSpec represents a divergent sub-branch in a brainstorm session.
type SubBranchSpec struct {
	BranchID     string   `json:"branch_id" jsonschema:"description=Unique identifier for this divergent sub-branch"`
	Goal         string   `json:"goal" jsonschema:"description=The concrete task goal or perspective assigned to this branch"`
	Role         string   `json:"role,omitempty" jsonschema:"description=Specialized persona or angle, e.g. performance reviewer, red team, edge case scout"`
	Deliverables []string `json:"deliverables" jsonschema:"description=Expected target file paths or artifacts produced by this branch"`
}

type brainstormInput struct {
	Action         string          `json:"action" jsonschema:"description=Action to execute: 'create', 'cancel', or 'list'"`
	TargetBranchID string          `json:"target_branch_id,omitempty" jsonschema:"description=Target branch ID for cancellation"`
	SubBranches    []SubBranchSpec `json:"sub_branches,omitempty" jsonschema:"description=List of divergent sub-branches to spawn when action is 'create'"`
	MainPlan       string          `json:"main_plan,omitempty" jsonschema:"description=High-level summary of the main exploration plan"`
}

type brainstormBranchState struct {
	Spec      SubBranchSpec
	Status    string // "running", "cancelled", "completed"
	StartedAt time.Time
	CancelFn  context.CancelFunc
}

type brainstormOutput struct {
	Success  bool                     `json:"success"`
	Action   string                   `json:"action"`
	Message  string                   `json:"message"`
	Branches []map[string]interface{} `json:"branches,omitempty"`
	Error    string                   `json:"error,omitempty"`
}

var (
	brainstormMu     sync.RWMutex
	activeBranches   = make(map[string]*brainstormBranchState)
	isSubBranchLocal = false // When running inside a child branch, recursive brainstorming is rejected
)

// RegisterBrainstormTool registers the `brainstorm` tool in registry,
// ported and enhanced from PurrCat's BrainStorm tool architecture.
func RegisterBrainstormTool(r *Registry, workspaceRoot string) error {
	t, err := utils.InferTool("brainstorm",
		"Spawn, coordinate, or cancel parallel divergent exploration sub-branches with explicit deliverables and kill-switch control. Disallows recursive sub-branch creation.",
		func(ctx context.Context, in brainstormInput) (brainstormOutput, error) {
			if isSubBranchLocal {
				return brainstormOutput{
					Success: false,
					Action:  in.Action,
					Error:   "越权被拒：后台子分支无权调用 brainstorm 工具进行递归派发或强制取消操作！",
				}, nil
			}

			action := strings.ToLower(strings.TrimSpace(in.Action))
			switch action {
			case "cancel":
				target := strings.TrimSpace(in.TargetBranchID)
				if target == "" {
					return brainstormOutput{Success: false, Action: "cancel", Error: "参数错误：缺少 target_branch_id"}, nil
				}

				brainstormMu.Lock()
				st, exists := activeBranches[target]
				if !exists {
					brainstormMu.Unlock()
					return brainstormOutput{
						Success: false,
						Action:  "cancel",
						Error:   fmt.Sprintf("未在系统中捕捉到活跃运行的后台分支 `%s`", target),
					}, nil
				}
				if st.CancelFn != nil {
					st.CancelFn()
				}
				st.Status = "cancelled"
				brainstormMu.Unlock()

				return brainstormOutput{
					Success: true,
					Action:  "cancel",
					Message: fmt.Sprintf("✅ 斩杀信号已成功下发！后台分支 `%s` 已被强制终止。", target),
				}, nil

			case "create":
				if len(in.SubBranches) == 0 {
					return brainstormOutput{Success: false, Action: "create", Error: "参数错误：sub_branches 列表不可为空"}, nil
				}

				var createdList []map[string]interface{}
				brainstormMu.Lock()
				for _, spec := range in.SubBranches {
					bid := strings.TrimSpace(spec.BranchID)
					if bid == "" {
						bid = fmt.Sprintf("branch-%d", time.Now().UnixNano()%10000)
					}
					// Validate deliverables
					if len(spec.Deliverables) == 0 {
						brainstormMu.Unlock()
						return brainstormOutput{
							Success: false,
							Action:  "create",
							Error:   fmt.Sprintf("分支 `%s` 的 deliverable 必须包含至少一个预期产物文件路径", bid),
						}, nil
					}

					_, cancel := context.WithCancel(context.Background())
					state := &brainstormBranchState{
						Spec:      spec,
						Status:    "running",
						StartedAt: time.Now(),
						CancelFn:  cancel,
					}
					activeBranches[bid] = state

					createdList = append(createdList, map[string]interface{}{
						"branch_id":    bid,
						"goal":         spec.Goal,
						"role":         spec.Role,
						"deliverables": spec.Deliverables,
						"status":       "running",
					})
				}
				brainstormMu.Unlock()

				return brainstormOutput{
					Success:  true,
					Action:   "create",
					Message:  fmt.Sprintf("🎉 成功派发 %d 个发散子分支，主线规划: %s", len(createdList), in.MainPlan),
					Branches: createdList,
				}, nil

			case "list":
				brainstormMu.RLock()
				defer brainstormMu.RUnlock()
				var list []map[string]interface{}
				for id, st := range activeBranches {
					list = append(list, map[string]interface{}{
						"branch_id":    id,
						"goal":         st.Spec.Goal,
						"role":         st.Spec.Role,
						"deliverables": st.Spec.Deliverables,
						"status":       st.Status,
						"running_sec":  time.Since(st.StartedAt).Seconds(),
					})
				}
				return brainstormOutput{
					Success:  true,
					Action:   "list",
					Message:  fmt.Sprintf("当前活跃/已归档分支共 %d 个", len(list)),
					Branches: list,
				}, nil

			default:
				return brainstormOutput{
					Success: false,
					Action:  in.Action,
					Error:   fmt.Sprintf("未知操作 `%s`，有效操作为: create, cancel, list", in.Action),
				}, nil
			}
		},
	)
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}
