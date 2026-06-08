package admin

import (
	"context"
	"fmt"
	"strings"
)

// Planner produces multi-step plans for durable admin tasks.
type Planner interface {
	PlanSteps(ctx context.Context, goal string) ([]string, error)
	ReplanSteps(ctx context.Context, goal, failedStep, errMsg string) ([]string, error)
}

// LLMPlanner uses an LLM to decompose goals (PyBot AdminPlanner subset).
type LLMPlanner struct {
	call func(ctx context.Context, system, user string) (string, error)
}

func NewLLMPlanner(call func(ctx context.Context, system, user string) (string, error)) *LLMPlanner {
	return &LLMPlanner{call: call}
}

const adminPlanSystem = `你是管理员任务规划器。将用户目标拆成 3–6 个可执行步骤。
每行一个步骤，以 "1." "2." 编号；不要多余解释。`

const adminReplanSystem = `你是管理员任务重规划器。当执行用户目标的某个步骤失败时，你需要生成一个新的多步骤计划来修复错误并完成剩余任务。
根据报错和当前已失败步骤，调整并输出新的 3-6 个可执行步骤。
每行一个步骤，以 "1." "2." 编号；不要多余解释。`

func (p *LLMPlanner) PlanSteps(ctx context.Context, goal string) ([]string, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return DefaultPlanSteps(goal), nil
	}
	if p == nil || p.call == nil {
		return DefaultPlanSteps(goal), nil
	}
	out, err := p.call(ctx, adminPlanSystem, "目标：\n"+goal)
	if err != nil || strings.TrimSpace(out) == "" {
		return DefaultPlanSteps(goal), err
	}
	steps := parseNumberedSteps(out)
	if len(steps) < 2 {
		return DefaultPlanSteps(goal), nil
	}
	return steps, nil
}

func (p *LLMPlanner) ReplanSteps(ctx context.Context, goal, failedStep, errMsg string) ([]string, error) {
	if goal == "" {
		return ReplanAfterFailure(goal, failedStep, errMsg), nil
	}
	if p == nil || p.call == nil {
		return ReplanAfterFailure(goal, failedStep, errMsg), nil
	}
	prompt := fmt.Sprintf("总目标：%s\n已失败步骤：%s\n错误信息：%s", goal, failedStep, errMsg)
	out, err := p.call(ctx, adminReplanSystem, prompt)
	if err != nil || strings.TrimSpace(out) == "" {
		return ReplanAfterFailure(goal, failedStep, errMsg), err
	}
	steps := parseNumberedSteps(out)
	if len(steps) < 2 {
		return ReplanAfterFailure(goal, failedStep, errMsg), nil
	}
	return steps, nil
}

func parseNumberedSteps(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.IndexAny(line, ".)"); idx > 0 && idx < 4 {
			line = strings.TrimSpace(line[idx+1:])
			line = strings.TrimLeft(line, ")")
		}
		line = strings.TrimPrefix(line, "、")
		line = strings.TrimLeft(line, "-*• ")
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// HeuristicPlanner always uses DefaultPlanSteps.
type HeuristicPlanner struct{}

func (HeuristicPlanner) PlanSteps(_ context.Context, goal string) ([]string, error) {
	return DefaultPlanSteps(goal), nil
}

func (HeuristicPlanner) ReplanSteps(_ context.Context, goal, failedStep, errMsg string) ([]string, error) {
	return ReplanAfterFailure(goal, failedStep, errMsg), nil
}

// PlanGoal resolves steps via planner or heuristic fallback.
func PlanGoal(ctx context.Context, planner Planner, goal string) ([]string, error) {
	if planner == nil {
		return DefaultPlanSteps(goal), nil
	}
	steps, err := planner.PlanSteps(ctx, goal)
	if err != nil {
		return steps, fmt.Errorf("planner: %w", err)
	}
	if len(steps) == 0 {
		return DefaultPlanSteps(goal), nil
	}
	return steps, nil
}

// DefaultPlanSteps splits a goal into executable steps (heuristic fallback).
func DefaultPlanSteps(goal string) []string {
	g := strings.TrimSpace(goal)
	if g == "" {
		return []string{"（空目标）"}
	}
	parts := splitGoalParts(g)
	if len(parts) >= 2 {
		return parts
	}
	return []string{
		"分析目标并列出子任务：" + g,
		"执行必要工具/工作流并完成：" + g,
		"汇总结果与后续建议：" + g,
	}
}

func splitGoalParts(goal string) []string {
	for _, sep := range []string{"；", ";", "\n", "。"} {
		if strings.Contains(goal, sep) {
			var out []string
			for _, p := range strings.FieldsFunc(goal, func(r rune) bool {
				return r == '；' || r == ';' || r == '\n'
			}) {
				p = strings.TrimSpace(p)
				if p != "" {
					out = append(out, p)
				}
			}
			if len(out) >= 2 {
				return out
			}
		}
	}
	return nil
}
