package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBrainstormTool(t *testing.T) {
	r := NewRegistry()
	assert.NoError(t, RegisterBrainstormTool(r, "/test/workspace"))

	// 1. Create sub-branches
	createJSON := `{
		"action": "create",
		"main_plan": "测试发散搜索与设计",
		"sub_branches": [
			{
				"branch_id": "sub-1",
				"goal": "探索算法瓶颈",
				"role": "性能评估专家",
				"deliverables": ["docs/perf.md"]
			},
			{
				"branch_id": "sub-2",
				"goal": "探索边界与异常用例",
				"role": "红队专家",
				"deliverables": ["docs/edge_cases.md"]
			}
		]
	}`

	res, err := r.InvokeJSON(context.Background(), "brainstorm", createJSON)
	assert.NoError(t, err)
	assert.Contains(t, res, "成功派发 2 个发散子分支")
	assert.Contains(t, res, "sub-1")
	assert.Contains(t, res, "sub-2")

	// 2. List branches
	listRes, err := r.InvokeJSON(context.Background(), "brainstorm", `{"action":"list"}`)
	assert.NoError(t, err)
	assert.Contains(t, listRes, "sub-1")

	// 3. Cancel branch
	cancelRes, err := r.InvokeJSON(context.Background(), "brainstorm", `{"action":"cancel","target_branch_id":"sub-1"}`)
	assert.NoError(t, err)
	assert.Contains(t, cancelRes, "斩杀信号已成功下发")
	assert.Contains(t, cancelRes, "sub-1")
}
