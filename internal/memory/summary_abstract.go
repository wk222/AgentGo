package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentgo/internal/promptbudget"
)

// Why a model answer was not used as a node abstract. The excerpt stays in place,
// so these are only counted and logged for whoever wonders why a branch is not
// model-written.
const (
	rejectCall     = "model call failed"
	rejectParse    = "answer was not the requested JSON"
	rejectChildren = "answer did not cover every child exactly once"
	rejectEmpty    = "empty abstract"
	rejectLong     = "abstract over the token limit"
)

type abstractChild struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Summary string `json:"summary"`
}

type abstractAnswer struct {
	Summary  string   `json:"summary"`
	Children []string `json:"covered_children"`
}

// parseAbstract accepts only an answer that is the requested JSON, covers every
// supplied child exactly once and says something.
func parseAbstract(answer string, ids []string) (string, string) {
	var parsed abstractAnswer
	if json.Unmarshal([]byte(cleanJSONResponse(answer)), &parsed) != nil {
		return "", rejectParse
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	ok := len(parsed.Children) == len(ids)
	for _, id := range parsed.Children {
		if !wanted[id] {
			ok = false
		}
		delete(wanted, id)
	}
	if !ok || len(wanted) != 0 {
		return "", rejectChildren
	}
	summary := strings.TrimSpace(parsed.Summary)
	if summary == "" {
		return "", rejectEmpty
	}
	return summary, ""
}

// fitToLimit cuts a too-long abstract back to its last whole clause that fits. It
// refuses when no clause boundary fits or when more than half would be lost: a
// stump is worse than the excerpt it would replace.
func fitToLimit(text string, limit int) (string, bool) {
	if promptbudget.Estimate(text) <= limit {
		return text, true
	}
	runes := []rune(text)
	best := 0
	for i, r := range runes {
		boundary := strings.ContainsRune("。；;！？!?", r) || (r == '.' && (i+1 == len(runes) || runes[i+1] == ' '))
		if !boundary {
			continue
		}
		if promptbudget.Estimate(string(runes[:i+1])) > limit {
			break
		}
		best = i + 1
	}
	if best == 0 || best*2 < len(runes) {
		return "", false
	}
	return strings.TrimSpace(string(runes[:best])), true
}

// writeAbstract asks the model for the abstract of one node and returns it with
// the number of model calls it took. A model cannot count this package's token
// estimate, and a dense node routinely comes back a little over the limit; one
// such answer would otherwise be thrown away together with every ancestor that
// waits for it. So an answer that is right but too long gets exactly one request
// to shorten it, with its measured size, and if it is still over, the shorter of
// the two is cut back to its last whole clause. A blank answer means no model is
// configured and is not a failure.
func writeAbstract(ctx context.Context, caller LLMCaller, children []SummaryNode, childText string) (summary string, calls int, rejected string) {
	input := make([]abstractChild, 0, len(children))
	ids := make([]string, 0, len(children))
	for _, c := range children {
		input = append(input, abstractChild{c.ID, c.Label, c.Summary})
		ids = append(ids, c.ID)
	}
	raw, _ := json.Marshal(input)
	// An abstract must be shorter than what it replaces, and within the node budget.
	limit := min(summaryNodeTokens, promptbudget.Estimate(childText)-1)
	if limit < 8 {
		return "", 0, "" // the excerpt is already as short as any abstract could be
	}
	ask := func(user string) (text, why string, blank bool) {
		answer, err := caller(ctx, treeSummarySystem, user)
		calls++
		switch {
		case ctx.Err() != nil:
			return "", "", true
		case err != nil:
			return "", rejectCall, false
		case strings.TrimSpace(answer) == "":
			return "", "", true
		}
		text, why = parseAbstract(answer, ids)
		return text, why, false
	}

	first, why, blank := ask(string(raw))
	if blank {
		return "", calls, ""
	}
	if why != "" {
		return "", calls, why
	}
	size := promptbudget.Estimate(first)
	if size <= limit {
		return first, calls, ""
	}

	again, _, blank := ask(fmt.Sprintf("%s\n\nYour abstract was %d estimated tokens; the limit is %d. Write it again at about %d%% of that length, in the same JSON format, still covering every child.",
		raw, size, limit, max(10, limit*85/size)))
	if ctx.Err() != nil {
		return "", calls, ""
	}
	if !blank && again != "" && promptbudget.Estimate(again) <= limit {
		return again, calls, ""
	}
	candidate := first
	if again != "" && promptbudget.Estimate(again) < size {
		candidate = again
	}
	if fitted, ok := fitToLimit(candidate, limit); ok {
		return fitted, calls, ""
	}
	return "", calls, rejectLong
}

// rejectionNote summarizes the counted reasons for one log line.
func rejectionNote(reasons map[string]int) string {
	var parts []string
	for _, why := range []string{rejectCall, rejectParse, rejectChildren, rejectEmpty, rejectLong} {
		if n := reasons[why]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s ×%d", why, n))
		}
	}
	return strings.Join(parts, "; ")
}
