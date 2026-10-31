package bridge

import (
	"context"
	"errors"
	"strings"
	"time"

	"agentgo/internal/config"
	"agentgo/internal/memory"
	"agentgo/internal/promptbudget"
)

// The memory console draws the same summary tree the model navigates. These
// methods only read it, except RefreshMemoryTree, which runs the refresh the
// background worker would run anyway.

func (s *AppService) memoryForest() *memory.SummaryForest {
	if p := s.rt.memoryPipeline(); p != nil {
		return p.SummaryForest()
	}
	return nil
}

func memoryTreeScope(scope string) string {
	if scope = strings.TrimSpace(scope); scope != "" {
		return scope
	}
	return "global"
}

// GetMemoryTree returns the tree overview (empty nodeID) or one node with its
// direct children (nodeID from an earlier reply). Scope is a conversation id or
// "global"; a conversation's tree includes the global memories.
func (s *AppService) GetMemoryTree(scope, nodeID string) map[string]any {
	f := s.memoryForest()
	if f == nil {
		return map[string]any{"success": false, "error": "记忆服务不可用"}
	}
	scope = memoryTreeScope(scope)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if strings.TrimSpace(nodeID) == "" {
		ov, err := f.Overview(ctx, scope)
		if err != nil {
			return map[string]any{"success": false, "error": err.Error()}
		}
		return map[string]any{"success": true, "overview": ov, "hierarchy": config.MemoryHierarchy.On()}
	}
	node, err := f.Subtree(ctx, scope, nodeID)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "node": node}
}

// LocateMemoryTreeNode returns the chain from the root down to a node (each with
// its direct children), so a node deep in the tree can be opened in one call.
func (s *AppService) LocateMemoryTreeNode(scope, nodeID string) map[string]any {
	f := s.memoryForest()
	if f == nil {
		return map[string]any{"success": false, "error": "记忆服务不可用"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	chain, err := f.Locate(ctx, memoryTreeScope(scope), strings.TrimSpace(nodeID))
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "chain": chain}
}

// RefreshMemoryTree rebuilds the tree structure now. Model-written abstracts are
// filled in afterwards in the background, so the reply is quick.
func (s *AppService) RefreshMemoryTree(scope string) map[string]any {
	f := s.memoryForest()
	if f == nil {
		return map[string]any{"success": false, "error": "记忆服务不可用"}
	}
	scope = memoryTreeScope(scope)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := f.RebuildNow(ctx, scope); err != nil {
		if errors.Is(err, memory.ErrSourcesChanged) {
			return map[string]any{"success": false, "error": "记忆刚有变动，请再点一次刷新"}
		}
		return map[string]any{"success": false, "error": err.Error()}
	}
	ov, err := f.Overview(ctx, scope)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "overview": ov, "hierarchy": config.MemoryHierarchy.On()}
}

// PreviewMemoryContext renders the memory prompt a run would receive for query,
// through the same automatic-recall path as a real run, and lists the tree nodes
// inside it so the console can mark them. tokens <= 0 uses the automatic budget.
func (s *AppService) PreviewMemoryContext(scope, query string, tokens int) map[string]any {
	engine := s.rt.Memory()
	if engine == nil {
		return map[string]any{"success": false, "error": "记忆服务不可用"}
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return map[string]any{"success": false, "error": "请先写一句任务描述，模型每次看到的记忆取决于它"}
	}
	scope = memoryTreeScope(scope)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = memory.WithRecallQuery(ctx, query)
	if tokens > 0 {
		ctx = memory.WithPromptBudget(ctx, tokens)
	}
	prompt, err := engine.ContextPrompt(ctx, scope)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	ids := memory.FrontierNodeIDs(prompt)
	nodes := make([]memory.TreeNodeView, 0, len(ids))
	if f := s.memoryForest(); f != nil {
		for _, id := range ids {
			// A node that left the snapshot between the prompt and now is simply not drawn.
			if n, e := f.Peek(ctx, scope, id); e == nil {
				nodes = append(nodes, n)
			}
		}
	}
	return map[string]any{
		"success": true, "scope": scope, "query": query,
		"budget": memory.PromptTokenBudget(ctx), "tokens": promptbudget.Estimate(prompt),
		"hierarchy": config.MemoryHierarchy.On(), "prompt": prompt, "node_ids": ids, "nodes": nodes,
	}
}
