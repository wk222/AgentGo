package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// LoopNodeExecutor runs a sub-flowgram for each item in a collection (Coze Loop).
type LoopNodeExecutor struct{}

func (e *LoopNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	return runSubFlowgramItems(ctx, n, input, last, rc, false)
}

// BatchNodeExecutor runs sub-flowgram on collection chunks (Coze Batch).
type BatchNodeExecutor struct{}

func (e *BatchNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	return runSubFlowgramItems(ctx, n, input, last, rc, true)
}

func runSubFlowgramItems(ctx context.Context, n Node, input, last string, rc RunContext, batch bool) (string, error) {
	raw := subFlowgramJSON(n)
	if raw == "" {
		return "", fmt.Errorf("%s node %s: missing sub_canvas/sub_flowgram", n.Type, n.ID)
	}
	var doc FlowgramDocument
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return "", fmt.Errorf("%s node %s: invalid sub_flowgram: %w", n.Type, n.ID, err)
	}
	subDef := doc.ToDefinition(n.ID+"_body", "")

	collection := last
	if n.Config != nil {
		if cv, ok := n.Config["collection_var"].(string); ok && strings.TrimSpace(cv) != "" {
			cv = strings.TrimSpace(cv)
			if v, ok := rc.Vars[cv]; ok && strings.TrimSpace(v) != "" {
				collection = v
			} else {
				collection = expandTemplateVars("{{var."+cv+"}}", input, last, rc.Vars)
			}
		} else if c, ok := n.Config["collection"].(string); ok && strings.TrimSpace(c) != "" {
			collection = expandTemplateVars(c, input, last, rc.Vars)
		}
	}
	items, err := parseCollection(collection)
	if err != nil {
		return "", fmt.Errorf("%s node %s: %w", n.Type, n.ID, err)
	}

	itemVar := "item"
	if n.Config != nil {
		if v, ok := n.Config["item_var"].(string); ok && strings.TrimSpace(v) != "" {
			itemVar = strings.TrimSpace(v)
		}
	}
	maxIter := 100
	if n.Config != nil {
		maxIter = intFromConfig(n.Config, "max_iterations", maxIter)
	}
	batchSize := 5
	if batch && n.Config != nil {
		batchSize = intFromConfig(n.Config, "batch_size", batchSize)
		if batchSize < 1 {
			batchSize = 1
		}
	}

	if rc.Vars == nil {
		rc.Vars = make(map[string]string)
	}

	results := make([]string, 0)
	if batch {
		for i := 0; i < len(items); i += batchSize {
			end := i + batchSize
			if end > len(items) {
				end = len(items)
			}
			chunk := items[i:end]
			b, _ := json.Marshal(chunk)
			rc.Vars[itemVar] = string(b)
			subRC := rc
			subRC.Input = string(b)
			out, err := Execute(ctx, subDef, subRC)
			if err != nil {
				return "", fmt.Errorf("%s batch %d: %w", n.Type, i/batchSize, err)
			}
			results = append(results, out)
			if len(results) >= maxIter {
				break
			}
		}
	} else {
		for i, it := range items {
			if i >= maxIter {
				break
			}
			itemStr := fmt.Sprint(it)
			rc.Vars[itemVar] = itemStr
			subRC := rc
			subRC.Input = itemStr
			out, err := Execute(ctx, subDef, subRC)
			if err != nil {
				return "", fmt.Errorf("%s item %d: %w", n.Type, i, err)
			}
			results = append(results, out)
		}
	}
	b, _ := json.Marshal(results)
	return string(b), nil
}

func subFlowgramJSON(n Node) string {
	if n.Config == nil {
		return ""
	}
	if s, ok := n.Config["sub_flowgram"].(string); ok && strings.TrimSpace(s) != "" {
		return s
	}
	if s, ok := n.Config["sub_canvas"].(string); ok {
		return s
	}
	return ""
}

func parseCollection(raw string) ([]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty collection")
	}
	var items []any
	if err := json.Unmarshal([]byte(raw), &items); err == nil && len(items) > 0 {
		return items, nil
	}
	// Single scalar treated as one-item collection
	return []any{raw}, nil
}

func intFromConfig(cfg map[string]any, key string, def int) int {
	switch v := cfg[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return i
		}
	}
	return def
}
