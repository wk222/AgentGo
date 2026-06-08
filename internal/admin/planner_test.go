package admin

import (
	"context"
	"testing"
)

func TestParseNumberedSteps(t *testing.T) {
	steps := parseNumberedSteps("1. 分析\n2. 执行\n3. 汇报")
	if len(steps) != 3 {
		t.Fatalf("got %d steps", len(steps))
	}
}

func TestPlanGoalHeuristic(t *testing.T) {
	steps, err := PlanGoal(context.Background(), nil, "修复应用 A；验证 B")
	if err != nil || len(steps) < 2 {
		t.Fatalf("steps=%v err=%v", steps, err)
	}
}

func TestHeuristicReplan(t *testing.T) {
	planner := HeuristicPlanner{}
	steps, err := planner.ReplanSteps(context.Background(), "修复应用 A", "步骤2", "报错信息")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) < 2 {
		t.Fatalf("expected at least 2 steps, got %v", steps)
	}
}

func TestLLMReplan(t *testing.T) {
	mockCallCalled := false
	mockCall := func(ctx context.Context, system, user string) (string, error) {
		mockCallCalled = true
		return "1. 发现错误\n2. 修复代码\n3. 重新测试", nil
	}
	planner := NewLLMPlanner(mockCall)
	steps, err := planner.ReplanSteps(context.Background(), "修复错误", "步骤1", "编译失败")
	if err != nil {
		t.Fatal(err)
	}
	if !mockCallCalled {
		t.Fatal("expected mock call to be called")
	}
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	if steps[0] != "发现错误" || steps[1] != "修复代码" || steps[2] != "重新测试" {
		t.Errorf("unexpected steps: %v", steps)
	}
}
