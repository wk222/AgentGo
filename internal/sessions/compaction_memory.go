package sessions

import (
	"agentgo/internal/promptbudget"
	"encoding/json"
	"github.com/cloudwego/eino/schema"
	"strings"
)

const extraProjectionMemory = "agentgo.projection_memory.v1"

type ProjectionMemory struct {
	MemoryRevision string     `json:"memory_revision,omitempty"`
	State          *TaskState `json:"state,omitempty"`
	Goal           string
	Entries        []string
	PinnedRequests []string
	UserNotes      []string
	Summaries      []string
	RecalledMemory []string
	Files          *FileLedger `json:"files,omitempty"`
}

func (p *ProjectedRuntimeView) inheritMemory(msg *schema.Message) {
	if msg.Extra == nil {
		return
	}
	raw, ok := msg.Extra[extraProjectionMemory].(string)
	if !ok {
		return
	}
	var m ProjectionMemory
	if json.Unmarshal([]byte(raw), &m) != nil {
		return
	}
	if p.memory.MemoryRevision == "" {
		p.memory.MemoryRevision = m.MemoryRevision
	}
	if p.memory.Goal == "" {
		p.memory.Goal = m.Goal
	}
	p.memory.Entries = append(p.memory.Entries, m.Entries...)
	p.memory.UserNotes = append(p.memory.UserNotes, m.UserNotes...)
	p.memory.PinnedRequests = append(p.memory.PinnedRequests, m.PinnedRequests...)
	p.memory.Summaries = append(p.memory.Summaries, m.Summaries...)
	p.memory.RecalledMemory = append(p.memory.RecalledMemory, m.RecalledMemory...)
	if !m.Files.Empty() {
		if p.memory.Files == nil {
			p.memory.Files = &FileLedger{}
		}
		p.memory.Files.merge(m.Files)
	}
}
func boundedUnique(lines []string, limit int) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len([]rune(line)) > limit {
			line = excerpt(line, limit-40)
		}
		if line != "" && !seen[line] {
			seen[line] = true
			out = append(out, line)
		}
	}
	used := 0
	start := len(out)
	for start > 0 {
		n := len([]rune(out[start-1]))
		if used+n > limit {
			break
		}
		used += n
		start--
	}
	return append([]string(nil), out[start:]...)
}

// excerpt preserves both ends and never cuts through UTF-8. This is a recovery
// checkpoint, not an assertion that every historical detail fits in the prompt.
func excerpt(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	head := n * 2 / 3
	return string(r[:head]) + " …[历史摘录已缩短]… " + string(r[len(r)-(n-head):])
}
func (p *ProjectedRuntimeView) rememberCompaction(raw, kept []FlatMessage, dropped int) {
	if dropped > 0 && p.memory.Goal == "" {
		for _, m := range raw {
			if m.Role == "user" {
				p.memory.Goal = excerpt(m.Content, 1200)
				break
			}
		}
	}
	keptCounts := map[string]int{}
	for _, m := range kept {
		keptCounts[flatMemoryKey(m)]++
	}
	for _, m := range raw {
		key := flatMemoryKey(m)
		if keptCounts[key] > 0 {
			keptCounts[key]--
			continue
		}
		var line string
		switch m.Role {
		case "user":
			if note := excerpt(m.Content, 1200); note != p.memory.Goal {
				p.memory.UserNotes = append(p.memory.UserNotes, "用户原话（历史）: "+note)
			}
		case "assistant":
			if m.Content != "" {
				line = "助手记录（待核实）: " + excerpt(m.Content, 400)
			}
			for _, tc := range m.ToolCalls {
				p.memory.Entries = append(p.memory.Entries, "工具调用（不代表成功）: "+summarizeToolCallForCheckpoint(tc.Function.Name, tc.Function.Arguments))
				if p.memory.Files == nil {
					p.memory.Files = &FileLedger{}
				}
				p.memory.Files.noteToolCall(tc.Function.Name, tc.Function.Arguments)
			}
		case "tool":
			line = "工具返回 " + m.ToolCallID + ": " + excerpt(m.Content, 600)
		}
		if strings.TrimSpace(line) != "" {
			p.memory.Entries = append(p.memory.Entries, line)
		}
	}
	p.memory.Entries = boundedUnique(p.memory.Entries, 3200)
	// Explicit constraint/correction cues retain the user's exact quote, not a
	// model-inferred instruction. Current messages still take precedence.
	for _, note := range p.memory.UserNotes {
		if explicitRequirement(note) {
			p.memory.PinnedRequests = append(p.memory.PinnedRequests, note)
		}
	}
	p.memory.PinnedRequests = boundedUnique(p.memory.PinnedRequests, 1600)
	p.memory.UserNotes = boundedUnique(p.memory.UserNotes, 4000)
	if !p.freshRecall {
		p.RecalledMemory = append(p.memory.RecalledMemory, p.RecalledMemory...)
	}
	recallBudget := 1000
	if p.recallTokenBudget != nil {
		recallBudget = max(0, min(32768, *p.recallTokenBudget))
	}
	p.RecalledMemory = boundedUnique(p.RecalledMemory, max(8000, recallBudget*4))
	if promptbudget.Estimate(strings.Join(p.RecalledMemory, "\n")) > recallBudget {
		p.RecalledMemory = recentPromptLines(p.RecalledMemory, recallBudget)
	}
	p.memory.RecalledMemory = append([]string(nil), p.RecalledMemory...)
	p.EpisodicSummary = recentPromptLines(boundedUnique(append(p.memory.Summaries, p.EpisodicSummary...), 6000), 500)
	p.memory.Summaries = append([]string(nil), p.EpisodicSummary...)
	if p.memory.Goal != "" || len(p.memory.UserNotes) > 0 || len(p.memory.Entries) > 0 || !p.memory.Files.Empty() {
		p.memory.State = buildTaskState(p.memory)
		p.TaskState = []string{p.memory.State.Prompt(1400)}
	}
}

func flatMemoryKey(m FlatMessage) string {
	var b strings.Builder
	b.WriteString(m.Role)
	b.WriteByte(0)
	b.WriteString(m.Content)
	b.WriteByte(0)
	b.WriteString(m.ToolCallID)
	for _, tc := range m.ToolCalls {
		b.WriteString(tc.ID)
		b.WriteString(tc.Function.Name)
		b.WriteString(tc.Function.Arguments)
	}
	return b.String()
}

// Pack the latest source summaries once; never summarize a rendered checkpoint.
func recentPromptLines(lines []string, budget int) []string {
	var out []string
	remaining := budget
	for i := len(lines) - 1; i >= 0; i-- {
		text := promptbudget.Excerpt(lines[i], remaining-1)
		if text == "" {
			break
		}
		remaining -= promptbudget.Estimate(text) + 1
		out = append(out, text)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
