package workflow

import (
	"context"
	"encoding/json"
	"strings"
)

// MergeNodeExecutor joins parallel branch outputs (Coze Merge / fan-in).
type MergeNodeExecutor struct{}

func (e *MergeNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	_ = ctx
	strategy := "json_array"
	if n.Config != nil {
		if s, ok := n.Config["merge_strategy"].(string); ok && strings.TrimSpace(s) != "" {
			strategy = strings.TrimSpace(s)
		}
	}
	parts := parseMergeParts(last, n, rc)
	if len(parts) == 0 {
		return last, nil
	}
	return applyMergeStrategy(strategy, parts), nil
}

func parseMergeParts(last string, n Node, rc RunContext) []string {
	if varKey := mergeVarKey(n); varKey != "" && rc.Vars != nil {
		if v, ok := rc.Vars[varKey]; ok && strings.TrimSpace(v) != "" {
			last = v
		}
	}
	last = strings.TrimSpace(last)
	if last == "" {
		return nil
	}
	var arr []string
	if err := json.Unmarshal([]byte(last), &arr); err == nil && len(arr) > 0 {
		return arr
	}
	return []string{last}
}

func mergeVarKey(n Node) string {
	if n.Config == nil {
		return ""
	}
	if pid, ok := n.Config["parallel_id"].(string); ok && pid != "" {
		return "parallel." + pid + ".results"
	}
	return ""
}

func applyMergeStrategy(strategy string, parts []string) string {
	switch strategy {
	case "concat":
		return strings.Join(parts, "\n---\n")
	case "first":
		return parts[0]
	case "last":
		return parts[len(parts)-1]
	default:
		b, _ := json.Marshal(parts)
		return string(b)
	}
}
