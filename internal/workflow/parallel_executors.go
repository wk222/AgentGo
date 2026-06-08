package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// ParallelNodeExecutor runs multiple branch sub-flowgrams concurrently (Coze Parallel).
type ParallelNodeExecutor struct{}

func (e *ParallelNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	branches := parallelBranches(n)
	if len(branches) == 0 {
		return last, fmt.Errorf("parallel node %s: no branches (connect outgoing edges)", n.ID)
	}
	merge := "json_array"
	maxWorkers := 4
	if n.Config != nil {
		if s, ok := n.Config["merge_strategy"].(string); ok && s != "" {
			merge = s
		}
		maxWorkers = intFromConfig(n.Config, "max_workers", maxWorkers)
		if maxWorkers < 1 {
			maxWorkers = 1
		}
	}
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	mu := sync.Mutex{}
	results := make([]string, len(branches))
	errs := make([]error, len(branches))

	for i, br := range branches {
		wg.Add(1)
		go func(i int, br map[string]any) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			raw, _ := br["flowgram"].(string)
			if strings.TrimSpace(raw) == "" {
				mu.Lock()
				errs[i] = fmt.Errorf("empty branch flowgram")
				mu.Unlock()
				return
			}
			var doc FlowgramDocument
			if err := json.Unmarshal([]byte(raw), &doc); err != nil {
				mu.Lock()
				errs[i] = err
				mu.Unlock()
				return
			}
			subDef := doc.ToDefinition(n.ID+"_parallel", "")
			subRC := rc
			subRC.Input = last
			out, err := Execute(ctx, subDef, subRC)
			mu.Lock()
			if err != nil {
				errs[i] = err
			} else {
				results[i] = out
			}
			mu.Unlock()
		}(i, br)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return "", fmt.Errorf("parallel node %s: %w", n.ID, err)
		}
	}
	b, _ := json.Marshal(results)
	out := string(b)
	if n.Config != nil {
		if mergeTarget, ok := n.Config["merge_target"].(string); ok && strings.TrimSpace(mergeTarget) != "" {
			if rc.Vars == nil {
				rc.Vars = make(map[string]string)
			}
			rc.Vars["parallel."+n.ID+".results"] = out
			return out, nil
		}
	}
	switch merge {
	case "concat":
		return strings.Join(results, "\n---\n"), nil
	default:
		return out, nil
	}
}

func parallelBranches(n Node) []map[string]any {
	if n.Config == nil {
		return nil
	}
	raw, ok := n.Config["parallel_branches"]
	if !ok {
		return nil
	}
	return coerceBranchMaps(raw)
}

func coerceBranchMaps(raw any) []map[string]any {
	switch v := raw.(type) {
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case []map[string]any:
		return v
	default:
		return nil
	}
}
