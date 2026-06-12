package workflow

import (
	"encoding/json"
	"strings"
)

type branchRule struct {
	ID        string `json:"id"`
	When      string `json:"when"`
	Target    string `json:"target"`
	IsDefault bool   `json:"isDefault"`
}

func ingestBranchTable(node *Node, data map[string]any) {
	if node == nil || data == nil {
		return
	}
	raw, ok := data["branch_table"].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return
	}
	var rows []branchRule
	if json.Unmarshal([]byte(raw), &rows) != nil {
		return
	}
	if node.Config == nil {
		node.Config = map[string]any{}
	}
	rules := make([]any, 0, len(rows))
	for _, r := range rows {
		rules = append(rules, map[string]any{
			"id":        r.ID,
			"when":      r.When,
			"target":    r.Target,
			"isDefault": r.IsDefault,
		})
	}
	node.Config["branch_rules"] = rules
}

func resolveWhenForEdge(node Node, e Edge) string {
	if w := strings.TrimSpace(e.When); w != "" {
		return w
	}
	if node.Config == nil {
		return ""
	}
	if rules, ok := node.Config["branch_rules"].([]any); ok {
		for _, raw := range rules {
			rule, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			target, _ := rule["target"].(string)
			if target != "" && target == e.To {
				if rw, ok := rule["when"].(string); ok {
					return rw
				}
			}
		}
	}
	if w, ok := node.Config["when"].(string); ok {
		return w
	}
	return ""
}

func orderEdgesByBranchRules(node Node, edges []Edge) []Edge {
	if node.Config == nil {
		return edges
	}
	rules, ok := node.Config["branch_rules"].([]any)
	if !ok || len(rules) == 0 {
		return edges
	}
	byTarget := map[string]Edge{}
	for _, e := range edges {
		byTarget[e.To] = e
	}
	ordered := make([]Edge, 0, len(edges))
	seen := map[string]bool{}
	for _, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		target, _ := rule["target"].(string)
		if target == "" {
			continue
		}
		if e, ok := byTarget[target]; ok && !seen[e.To] {
			ordered = append(ordered, e)
			seen[e.To] = true
		}
	}
	for _, e := range edges {
		if !seen[e.To] {
			ordered = append(ordered, e)
		}
	}
	return ordered
}

func isDefaultBranchEdge(node Node, e Edge) bool {
	if node.Config == nil {
		return false
	}
	rules, ok := node.Config["branch_rules"].([]any)
	if !ok {
		return false
	}
	for _, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		target, _ := rule["target"].(string)
		def, _ := rule["isDefault"].(bool)
		if def && target == e.To {
			return true
		}
	}
	return false
}
