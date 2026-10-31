package memory

import (
	"agentgo/internal/config"
	"agentgo/internal/promptbudget"
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

const treeContextHeader = "Memory tree: navigation abstracts, unverified. Use browse_memory action=tree with node IDs to expand.\n"

func summaryLocator(n SummaryNode) string { return fmt.Sprintf("[node=%s; n=%d] ", n.ID, n.Count) }
func minimumFrontierCost(nodes []SummaryNode) int {
	var b strings.Builder
	b.WriteString(treeContextHeader)
	for _, n := range nodes {
		b.WriteString(summaryLocator(n))
		b.WriteByte('\n')
	}
	return promptbudget.Estimate(b.String())
}

// Frontier returns an antichain covering the entire tree. Expanding a node
// replaces it with ALL its children, so no branch is silently lost or duplicated.
func (f *SummaryForest) Frontier(ctx context.Context, scope string, hits []Record, budget int) ([]SummaryNode, error) {
	root, revision, err := f.root(ctx, scope)
	if err != nil {
		return nil, err
	}
	if minimumFrontierCost([]SummaryNode{root}) > budget {
		if promptbudget.Estimate(summaryLocator(root)) <= budget {
			return []SummaryNode{root}, nil
		}
		return nil, nil
	}
	scores := map[string]float64{}
	for i, r := range hits {
		rows, e := f.store.db.QueryContext(ctx, "SELECT node_id FROM memory_tree_routes WHERE scope=? AND record_id=?", scope, r.ID)
		if e != nil {
			return nil, e
		}
		weight := 1.0 / float64(i+1)
		if r.IsCanonical {
			weight += 1
		}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return nil, e
			}
			scores[id] += weight
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	frontier := []SummaryNode{root}
	for visited := 0; visited < 128; visited++ {
		best := -1
		bestScore := -1.0
		var chosen []SummaryNode
		for i, n := range frontier {
			if len(n.Children) == 0 || len(frontier)+len(n.Children)-1 > 32 {
				continue
			}
			score := scores[n.ID]
			if len(hits) > 0 && score == 0 {
				continue
			}
			// With no hits, expose only the coarse root level.
			if len(hits) == 0 && n.ID != root.ID {
				continue
			}
			children := make([]SummaryNode, 0, len(n.Children))
			for _, id := range n.Children {
				child, e := f.node(ctx, scope, id)
				if e != nil {
					return nil, e
				}
				children = append(children, child)
			}
			proposed := append([]SummaryNode(nil), frontier[:i]...)
			proposed = append(proposed, children...)
			proposed = append(proposed, frontier[i+1:]...)
			cost := minimumFrontierCost(proposed)
			// Reserve enough room for useful text, not just a long directory of IDs.
			if cost > budget*2/3 {
				continue
			}
			gain := (score + 0.001) / float64(max(1, cost-minimumFrontierCost(frontier)))
			if gain > bestScore {
				best = i
				bestScore = gain
				chosen = proposed
			}
		}
		if best < 0 {
			break
		}
		frontier = chosen
	}
	// Most relevant nodes receive summary text first under the shared budget.
	sort.SliceStable(frontier, func(i, j int) bool { return scores[frontier[i].ID] > scores[frontier[j].ID] })
	current, e := f.store.TreeRevision(ctx, scope)
	if e != nil {
		return nil, e
	}
	if current != revision {
		return nil, ErrSourcesChanged
	}
	return frontier, nil
}
func renderSummaryFrontier(nodes []SummaryNode, budget int) string {
	if len(nodes) == 0 {
		return ""
	}
	lines := make([]string, len(nodes))
	for i, n := range nodes {
		lines[i] = summaryLocator(n)
	}
	render := func() string { return treeContextHeader + strings.Join(lines, "\n") }
	if promptbudget.Estimate(render()) > budget {
		if len(nodes) == 1 && promptbudget.Estimate(summaryLocator(nodes[0])) <= budget {
			return strings.TrimSpace(summaryLocator(nodes[0]))
		}
		return ""
	}
	for i, n := range nodes {
		available := max(0, budget-promptbudget.Estimate(render())-1)
		text := promptbudget.Excerpt(n.Summary, min(summaryNodeTokens, available))
		lines[i] += text
	}
	return strings.TrimSpace(render())
}
func (f *SummaryForest) ContextPrompt(ctx context.Context, scope string, hits []Record, budget int) string {
	if budget <= 0 {
		return ""
	}
	if f == nil || f.store == nil || !config.MemoryHierarchy.On() || !automaticRecall(ctx) {
		return PromptContext(ctx, hits, budget)
	}
	nodes, err := f.Frontier(ctx, scope, hits, budget)
	if err != nil {
		f.Request(ctx, scope)
		return PromptContext(ctx, hits, budget)
	}
	root, _, e := f.root(ctx, scope)
	if e == nil && !root.Complete {
		f.Request(ctx, scope)
	}
	return renderSummaryFrontier(nodes, budget)
}

// Browse only exposes members of the current scope's current tree snapshot.
func (f *SummaryForest) Browse(ctx context.Context, scope, id string, offset, limit int) ([]SummaryNode, int, error) {
	if offset < 0 || offset > 100000 {
		return nil, 0, fmt.Errorf("invalid tree offset")
	}
	root, revision, err := f.root(ctx, scope)
	if err != nil {
		f.Request(ctx, scope)
		return nil, 0, fmt.Errorf("memory summary tree is refreshing; direct search remains available: %w", err)
	}
	var nodes []SummaryNode
	if id == "" {
		nodes = []SummaryNode{root}
	} else {
		var exists int
		if err = f.store.db.QueryRowContext(ctx, "SELECT 1 FROM memory_tree_members WHERE scope=? AND id=?", scope, id).Scan(&exists); err != nil {
			if err == sql.ErrNoRows {
				return nil, 0, fmt.Errorf("node is not in the current memory scope")
			}
			return nil, 0, err
		}
		node, e := f.node(ctx, scope, id)
		if e != nil {
			return nil, 0, e
		}
		if len(node.Children) == 0 {
			nodes = []SummaryNode{node}
		} else {
			start := min(offset, len(node.Children))
			end := min(start+max(1, min(8, limit)), len(node.Children))
			for _, child := range node.Children[start:end] {
				n, e := f.node(ctx, scope, child)
				if e != nil {
					return nil, 0, e
				}
				nodes = append(nodes, n)
			}
			next := 0
			if end < len(node.Children) {
				next = end
			}
			current, e := f.store.TreeRevision(ctx, scope)
			if e != nil {
				return nil, 0, e
			}
			if current != revision {
				return nil, 0, ErrSourcesChanged
			}
			return nodes, next, nil
		}
	}
	if offset > 0 {
		return []SummaryNode{}, 0, nil
	}
	current, e := f.store.TreeRevision(ctx, scope)
	if e != nil {
		return nil, 0, e
	}
	if current != revision {
		return nil, 0, ErrSourcesChanged
	}
	return nodes, 0, nil
}
