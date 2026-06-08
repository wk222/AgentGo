package apps

import (
	"fmt"
	"strings"
)

// ValidateMatrixOrchestration checks node/edge consistency, required ports, cycles, and type contracts.
func ValidateMatrixOrchestration(m MatrixOrchestration) error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("orchestration name is empty")
	}
	if len(m.Nodes) == 0 {
		return fmt.Errorf("orchestration has no nodes")
	}
	ids := make(map[string]bool, len(m.Nodes))
	nodeMap := make(map[string]MatrixNode, len(m.Nodes))
	for _, n := range m.Nodes {
		if strings.TrimSpace(n.ID) == "" {
			return fmt.Errorf("node missing id")
		}
		if ids[n.ID] {
			return fmt.Errorf("duplicate node id: %s", n.ID)
		}
		ids[n.ID] = true
		nodeMap[n.ID] = n
		if strings.TrimSpace(n.AppID) == "" {
			return fmt.Errorf("node %s missing app_id", n.ID)
		}
	}

	for _, e := range m.Edges {
		if !ids[e.From] {
			return fmt.Errorf("edge from unknown node: %s", e.From)
		}
		if e.To != "" && !ids[e.To] {
			return fmt.Errorf("edge to unknown node: %s", e.To)
		}
		if err := validateTypedPortBinding(nodeMap, e); err != nil {
			return err
		}
	}

	// Check for cycles in the DAG
	if err := checkCycles(m.Nodes, m.Edges); err != nil {
		return err
	}

	// Check that all required inputs are connected
	if err := checkRequiredPorts(m.Nodes, m.Edges); err != nil {
		return err
	}

	return nil
}

func validateTypedPortBinding(nodes map[string]MatrixNode, e MatrixEdge) error {
	fromNode, ok1 := nodes[e.From]
	toNode, ok2 := nodes[e.To]
	if !ok1 || !ok2 {
		return nil
	}

	fp := strings.TrimSpace(e.FromPort)
	tp := strings.TrimSpace(e.ToPort)
	if fp == "" && tp == "" {
		return nil
	}
	if fp == "" || tp == "" {
		return fmt.Errorf("edge %s→%s: both from_port and to_port required when binding ports", e.From, e.To)
	}

	// Find source output port
	var sourcePort *Port
	for _, p := range fromNode.OutputPorts {
		if p.Name == fp {
			sourcePort = &p
			break
		}
	}

	// Find target input port
	var targetPort *Port
	for _, p := range toNode.InputPorts {
		if p.Name == tp {
			targetPort = &p
			break
		}
	}

	if sourcePort != nil && targetPort != nil {
		if sourcePort.DataType != targetPort.DataType {
			return fmt.Errorf("edge %s→%s: port data type mismatch. Output %s (%s) → Input %s (%s)",
				e.From, e.To, fp, sourcePort.DataType, tp, targetPort.DataType)
		}
	} else {
		// Fallback to legacy validation pattern match
		if fp != tp && !strings.HasPrefix(tp, fp+".") {
			return fmt.Errorf("edge %s→%s: port mismatch %q → %q", e.From, e.To, fp, tp)
		}
	}
	return nil
}

func checkCycles(nodes []MatrixNode, edges []MatrixEdge) error {
	adj := make(map[string][]string)
	for _, e := range edges {
		if e.To != "" {
			adj[e.From] = append(adj[e.From], e.To)
		}
	}
	visited := make(map[string]int) // 0: unvisited, 1: visiting, 2: visited
	var dfs func(node string) error
	dfs = func(node string) error {
		visited[node] = 1
		for _, next := range adj[node] {
			if visited[next] == 1 {
				return fmt.Errorf("cycle detected involving node: %s", next)
			}
			if visited[next] == 0 {
				if err := dfs(next); err != nil {
					return err
				}
			}
		}
		visited[node] = 2
		return nil
	}
	for _, n := range nodes {
		if visited[n.ID] == 0 {
			if err := dfs(n.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkRequiredPorts(nodes []MatrixNode, edges []MatrixEdge) error {
	nodeMap := make(map[string]MatrixNode)
	for _, n := range nodes {
		nodeMap[n.ID] = n
	}

	boundInputs := make(map[string]map[string]bool)
	for _, e := range edges {
		if e.To != "" && e.ToPort != "" {
			if boundInputs[e.To] == nil {
				boundInputs[e.To] = make(map[string]bool)
			}
			boundInputs[e.To][e.ToPort] = true
		}
	}

	for _, n := range nodes {
		for _, p := range n.InputPorts {
			if p.Required {
				if boundInputs[n.ID] == nil || !boundInputs[n.ID][p.Name] {
					return fmt.Errorf("node %s: required input port %q is not connected", n.ID, p.Name)
				}
			}
		}
	}
	return nil
}
