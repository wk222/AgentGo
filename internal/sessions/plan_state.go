package sessions

import (
	"agentgo/internal/promptbudget"
	"sort"
	"strings"
)

// PlanTaskSnapshot is a read projection of official Eino PlanTask storage.
// Completion is a planning assertion, not proof that a command or test succeeded.
type PlanTaskSnapshot struct {
	ID        string   `json:"id"`
	Subject   string   `json:"subject"`
	Status    string   `json:"status"`
	BlockedBy []string `json:"blockedBy"`
}

func (p *ProjectedRuntimeView) applyPlanTasks(tasks []PlanTaskSnapshot) {
	if len(tasks) == 0 {
		return
	}
	if p.memory.State == nil {
		p.memory.State = buildTaskState(p.memory)
	}
	p.memory.State.Plans = append([]PlanTaskSnapshot(nil), tasks...)
	sort.SliceStable(p.memory.State.Plans, func(i, j int) bool {
		rank := func(t PlanTaskSnapshot) int {
			if t.Status == "in_progress" {
				return 0
			}
			if t.Status == "pending" {
				return 1
			}
			return 2
		}
		return rank(p.memory.State.Plans[i]) < rank(p.memory.State.Plans[j])
	})
	p.TaskState = []string{p.memory.State.Prompt(1400)}
}

func (s *TaskState) planLines(budget int) []string {
	statuses := map[string]string{}
	for _, t := range s.Plans {
		statuses[t.ID] = t.Status
	}
	var out []string
	used := 0
	next := ""
	for _, t := range s.Plans {
		if t.Status != "in_progress" && t.Status != "pending" {
			continue
		}
		blocked := false
		for _, id := range t.BlockedBy {
			if statuses[id] != "completed" {
				blocked = true
				break
			}
		}
		if !blocked {
			next = "Next eligible plan #" + t.ID + ": " + promptbudget.Excerpt(t.Subject, 50)
			break
		}
	}
	if next != "" {
		out = append(out, next)
		used = promptbudget.Estimate(next + "\n")
	}
	for _, t := range s.Plans {
		if t.Status != "pending" && t.Status != "in_progress" && t.Status != "completed" {
			continue
		}
		line := "Plan #" + t.ID + " [" + t.Status + "]: " + promptbudget.Excerpt(t.Subject, 70)
		var blockers []string
		for _, id := range t.BlockedBy {
			if statuses[id] != "completed" {
				blockers = append(blockers, id)
			}
		}
		if len(blockers) > 0 {
			line += "; blocked_by=" + strings.Join(blockers[:min(6, len(blockers))], ",")
		}
		if t.Status == "completed" {
			line += " (reported; verification requires tool evidence)"
		}
		cost := promptbudget.Estimate(line + "\n")
		if used+cost > budget {
			continue
		}
		out = append(out, line)
		used += cost
	}
	return out
}
