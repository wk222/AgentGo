package sessions

import (
	"agentgo/internal/promptbudget"
	"crypto/sha256"
	"fmt"
	"strings"
)

// TaskState is a recovery projection, not an assertion that historical work succeeded.
// Structured entries are persisted in message metadata; only Prompt is model-visible.
type TaskState struct {
	Plans        []PlanTaskSnapshot `json:"plans,omitempty"`
	Goal         string             `json:"goal,omitempty"`
	Constraints  []StateNote        `json:"constraints,omitempty"`
	Requests     []StateNote        `json:"requests,omitempty"`
	Observations []StateNote        `json:"observations,omitempty"`
	Reports      []StateNote        `json:"reports,omitempty"`
	Files        *FileLedger        `json:"files,omitempty"`
}

type StateNote struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Text   string `json:"text"`
	Lookup string `json:"lookup,omitempty"`
}

func stateNote(source, text string) StateNote {
	sum := sha256.Sum256([]byte(source + "\x00" + text))
	// Lookup is a literal substring for read_session_history, not a fabricated record ID.
	runes := []rune(text)
	query := text
	if len(runes) > 32 {
		query = string(runes[:32])
	}
	return StateNote{ID: fmt.Sprintf("n%x", sum[:5]), Source: source, Text: text, Lookup: query}
}

func buildTaskState(m ProjectionMemory) *TaskState {
	s := &TaskState{Goal: m.Goal, Files: m.Files.clone()}
	pinned := map[string]bool{}
	for _, note := range m.PinnedRequests {
		pinned[note] = true
		s.Constraints = append(s.Constraints, stateNote("user/explicit-requirement", note))
	}
	for _, note := range m.UserNotes {
		if !pinned[note] {
			s.Requests = append(s.Requests, stateNote("user/history", note))
		}
	}
	for _, note := range m.Entries {
		// Keep legacy source labels without trying to infer semantic success from prose.
		if strings.HasPrefix(note, "\u5de5\u5177") {
			s.Observations = append(s.Observations, stateNote("tool/recorded", note))
		} else {
			s.Reports = append(s.Reports, stateNote("assistant/unverified", note))
		}
	}
	return s
}

// Prompt packs the state in priority order. Full notes remain in the event archive.
func (s *TaskState) Prompt(budget int) string {
	if s == nil || budget < 80 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Compaction Checkpoint] Historical state; current corrections override it. Tool records and assistant reports do not establish present success.\n")
	appendLine := func(line string) bool {
		if promptbudget.Estimate(b.String()+line+"\n") > budget {
			return false
		}
		b.WriteString(line)
		b.WriteByte('\n')
		return true
	}
	if s.Goal != "" {
		appendLine("Goal anchor: " + promptbudget.Excerpt(s.Goal, min(300, budget/4)))
	}
	pack := func(label string, notes []StateNote, sectionBudget int) {
		if len(notes) == 0 {
			return
		}
		used := 0
		var lines []string
		for i := len(notes) - 1; i >= 0; i-- {
			n := notes[i]
			// Reserve enough space for several requests rather than a single large note.
			prefix := label + ": "
			text := promptbudget.Excerpt(n.Text, min(180, sectionBudget-used-promptbudget.Estimate(prefix)-1))
			if text == "" {
				break
			}
			line := prefix + text
			cost := promptbudget.Estimate(line + "\n")
			if used+cost > sectionBudget {
				break
			}
			lines = append(lines, line)
			used += cost
		}
		for i := len(lines) - 1; i >= 0; i-- {
			appendLine(lines[i])
		}
	}
	constraintShare, requestShare, observationShare, reportShare := 25, 20, 22, 10
	if len(s.Plans) > 0 {
		for _, line := range s.planLines(budget * 22 / 100) {
			appendLine(line)
		}
		constraintShare, requestShare, observationShare, reportShare = 22, 14, 14, 6
	}
	pack("User constraint/correction (historical quote)", s.Constraints, budget*constraintShare/100)
	pack("User requirement (historical)", s.Requests, budget*requestShare/100)
	// Own budget share: paths must not compete with tool-result excerpts.
	for _, line := range s.Files.promptLines(budget * 12 / 100) {
		appendLine(line)
	}
	pack("Tool record (verify current state)", s.Observations, budget*observationShare/100)
	pack("Assistant report (unverified)", s.Reports, budget*reportShare/100)
	appendLine("Details: use short literal keywords with read_session_history; paginate original records.")
	return strings.TrimSpace(b.String())
}

// Cues only select quotes for retention; they never rewrite or elevate their authority.
func explicitRequirement(text string) bool {
	lower := strings.ToLower(text)
	for _, cue := range []string{"constraint:", "must ", "do not ", "never ", "preserve ", "correction:", "\u7ea6\u675f", "\u5fc5\u987b", "\u4e0d\u8981", "\u4fdd\u7559", "\u7981\u6b62", "\u4e0d\u5f97", "\u6682\u4e0d", "\u66f4\u6b63", "\u6539\u4e3a", "\u6539\u6210"} {
		if strings.Contains(lower, cue) {
			return true
		}
	}
	return false
}
