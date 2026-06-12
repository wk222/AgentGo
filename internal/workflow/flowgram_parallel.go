package workflow

import (
	"encoding/json"
)

// ExtractBranchSubFlowgram collects nodes/edges reachable from startID (BFS on main doc).
func ExtractBranchSubFlowgram(doc FlowgramDocument, startID string) FlowgramDocument {
	if startID == "" {
		return FlowgramDocument{}
	}
	nodeSet := map[string]FlowgramNode{}
	for _, n := range doc.Nodes {
		nodeSet[n.ID] = n
	}
	adj := map[string][]string{}
	for _, e := range doc.Edges {
		adj[e.SourceNodeID] = append(adj[e.SourceNodeID], e.TargetNodeID)
	}
	visited := map[string]bool{startID: true}
	q := []string{startID}
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		for _, to := range adj[cur] {
			if visited[to] {
				continue
			}
			visited[to] = true
			q = append(q, to)
		}
	}
	sub := FlowgramDocument{}
	for id := range visited {
		if n, ok := nodeSet[id]; ok {
			sub.Nodes = append(sub.Nodes, n)
		}
	}
	for _, e := range doc.Edges {
		if visited[e.SourceNodeID] && visited[e.TargetNodeID] {
			sub.Edges = append(sub.Edges, e)
		}
	}
	return sub
}

func attachParallelBranchFlowgrams(def *Definition, doc FlowgramDocument) {
	if def == nil {
		return
	}
	attachParallelMergeLinks(def, doc)
	out := map[string][]Edge{}
	for _, e := range def.Edges {
		out[e.From] = append(out[e.From], e)
	}
	for i := range def.Nodes {
		n := &def.Nodes[i]
		if n.Type != "parallel" {
			continue
		}
		if n.Config != nil {
			if _, ok := n.Config["merge_target"]; ok {
				continue
			}
			if _, ok := n.Config["parallel_branches"]; ok {
				continue
			}
		}
		edges := out[n.ID]
		branches := make([]any, 0, len(edges))
		for _, e := range edges {
			subDoc := ExtractBranchSubFlowgram(doc, e.To)
			b, _ := json.Marshal(subDoc)
			branches = append(branches, map[string]any{
				"target":   e.To,
				"flowgram": string(b),
				"when":     e.When,
			})
		}
		if n.Config == nil {
			n.Config = map[string]any{}
		}
		n.Config["parallel_branches"] = branches
		n.Config["parallel_targets"] = branchTargets(edges)
	}
}

func branchTargets(edges []Edge) []string {
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		out = append(out, e.To)
	}
	return out
}
