package workflow

import (
	"encoding/json"
	"strings"
)

// ExtractBranchSubFlowgramUntilMerge collects nodes from startID until mergeID (exclusive).
func ExtractBranchSubFlowgramUntilMerge(doc FlowgramDocument, startID, mergeID string) FlowgramDocument {
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
			if to == mergeID {
				continue
			}
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

func firstMergeReachable(doc FlowgramDocument, startID string) string {
	if startID == "" {
		return ""
	}
	typeName := map[string]string{}
	for _, n := range doc.Nodes {
		typeName[n.ID] = strings.ToLower(normalizeNodeType(n.Type))
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
		if typeName[cur] == "merge" {
			return cur
		}
		for _, to := range adj[cur] {
			if visited[to] {
				continue
			}
			visited[to] = true
			q = append(q, to)
		}
	}
	return ""
}

func commonMergeForBranches(doc FlowgramDocument, branchStarts []string) string {
	if len(branchStarts) == 0 {
		return ""
	}
	mergeHits := map[string]int{}
	for _, start := range branchStarts {
		mid := firstMergeReachable(doc, start)
		if mid != "" {
			mergeHits[mid]++
		}
	}
	for mid, cnt := range mergeHits {
		if cnt == len(branchStarts) {
			return mid
		}
	}
	return ""
}

func attachParallelMergeLinks(def *Definition, doc FlowgramDocument) {
	if def == nil {
		return
	}
	incoming := map[string][]string{}
	out := map[string][]Edge{}
	for _, e := range def.Edges {
		out[e.From] = append(out[e.From], e)
		incoming[e.To] = append(incoming[e.To], e.From)
	}
	for i := range def.Nodes {
		n := &def.Nodes[i]
		if n.Type != "parallel" {
			continue
		}
		edges := out[n.ID]
		if len(edges) == 0 {
			continue
		}
		targets := branchTargets(edges)
		mergeID := commonMergeForBranches(doc, targets)
		if mergeID == "" {
			continue
		}
		if n.Config == nil {
			n.Config = map[string]any{}
		}
		n.Config["merge_target"] = mergeID
		branches := make([]any, 0, len(edges))
		for _, e := range edges {
			subDoc := ExtractBranchSubFlowgramUntilMerge(doc, e.To, mergeID)
			b, _ := json.Marshal(subDoc)
			branches = append(branches, map[string]any{
				"target":   e.To,
				"flowgram": string(b),
				"when":     e.When,
				"merge":    mergeID,
			})
		}
		n.Config["parallel_branches"] = branches
		n.Config["parallel_targets"] = targets

		for j := range def.Nodes {
			if def.Nodes[j].ID != mergeID {
				continue
			}
			if def.Nodes[j].Config == nil {
				def.Nodes[j].Config = map[string]any{}
			}
			def.Nodes[j].Config["merge_sources"] = incoming[mergeID]
			def.Nodes[j].Config["parallel_id"] = n.ID
			break
		}
	}
}

func attachMergeIncoming(def *Definition) {
	if def == nil {
		return
	}
	incoming := map[string][]string{}
	for _, e := range def.Edges {
		incoming[e.To] = append(incoming[e.To], e.From)
	}
	for i := range def.Nodes {
		if def.Nodes[i].Type != "merge" {
			continue
		}
		if def.Nodes[i].Config == nil {
			def.Nodes[i].Config = map[string]any{}
		}
		if _, ok := def.Nodes[i].Config["merge_sources"]; !ok {
			def.Nodes[i].Config["merge_sources"] = incoming[def.Nodes[i].ID]
		}
	}
}
