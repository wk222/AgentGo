package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// This file is the read side of the summary tree for the memory console. It
// never writes the tree (except RebuildNow, which only runs the same Refresh the
// background worker runs) and never exposes a node outside the published snapshot.

// TreeRecordView is the evidence behind one leaf, bounded for the UI.
type TreeRecordView struct {
	Modality   string  `json:"modality"`
	Scope      string  `json:"scope"`
	Kind       string  `json:"kind"`
	Canonical  bool    `json:"canonical"`
	Importance float64 `json:"importance"`
	CreatedAt  int64   `json:"created_at"`
	Content    string  `json:"content"`
}

// TreeNodeView is one node as the console draws it.
type TreeNodeView struct {
	ID         string          `json:"id"`
	Label      string          `json:"label"`
	Summary    string          `json:"summary"`
	Count      int             `json:"count"`    // records below this node (1 for a record)
	Level      int             `json:"level"`    // 0 for a record, parents are one above their tallest child
	Complete   bool            `json:"complete"` // abstract written by the model; false = excerpt of the children
	RecordID   string          `json:"record_id,omitempty"`
	ChildCount int             `json:"child_count"`
	Children   []TreeNodeView  `json:"children,omitempty"`
	Record     *TreeRecordView `json:"record,omitempty"`
}

// TreeOverview describes the published tree of one scope.
type TreeOverview struct {
	Scope      string        `json:"scope"`
	Ready      bool          `json:"ready"` // a snapshot exists and matches the current memories
	Stale      bool          `json:"stale"` // a snapshot exists but memories changed after it was built
	Reason     string        `json:"reason,omitempty"`
	Refreshing bool          `json:"refreshing"`
	Records    int           `json:"records"`    // visible memories the tree should cover now
	Covered    int           `json:"covered"`    // records the published snapshot covers
	Nodes      int           `json:"nodes"`      // every node in the snapshot, records included
	Branches   int           `json:"branches"`   // nodes that summarize children
	Summarized int           `json:"summarized"` // branches whose abstract the model wrote
	Levels     int           `json:"levels"`     // layers from the root down to the records
	Fanout     int           `json:"fanout"`
	NodeTokens int           `json:"node_tokens"`
	Root       *TreeNodeView `json:"root,omitempty"` // the root with its direct children
}

const treeViewRecordRunes = 600

// publishedRoot reads the snapshot root even when the memories have changed since,
// so the console can still draw it and say it is out of date.
func (f *SummaryForest) publishedRoot(ctx context.Context, scope string) (n SummaryNode, built string, current string, err error) {
	if f == nil || f.store == nil {
		return n, "", "", errors.New("memory tree unavailable")
	}
	if current, err = f.store.TreeRevision(ctx, scope); err != nil {
		return n, "", "", err
	}
	var id string
	if err = f.store.db.QueryRowContext(ctx, "SELECT id,revision FROM memory_tree_roots WHERE scope=?", scope).Scan(&id, &built); err != nil {
		return n, "", current, err
	}
	n, err = f.node(ctx, scope, id)
	return n, built, current, err
}

func (f *SummaryForest) isRefreshing(scope string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[scope]
}

// Overview summarizes the tree and returns the root with its direct children.
func (f *SummaryForest) Overview(ctx context.Context, scope string) (TreeOverview, error) {
	out := TreeOverview{Scope: scope, Fanout: summaryFanout, NodeTokens: summaryNodeTokens}
	if f == nil || f.store == nil || strings.TrimSpace(scope) == "" {
		return out, errors.New("memory tree unavailable")
	}
	out.Refreshing = f.isRefreshing(scope)
	if err := f.store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memories m WHERE "+visibleMemory+" AND scope IN (?, 'global')", scope).Scan(&out.Records); err != nil {
		return out, err
	}
	root, built, current, err := f.publishedRoot(ctx, scope)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		out.Reason = "not_built"
		if out.Records == 0 {
			out.Reason = "empty"
		}
		return out, nil
	case err != nil:
		return out, err
	}
	out.Stale = built != current
	out.Ready = !out.Stale
	if out.Stale {
		out.Reason = "stale"
	}
	out.Covered = root.Count
	out.Levels = root.Level + 1
	// The leaf is the only node that carries a record id; a node holds children or a record, never both.
	if err = f.store.db.QueryRowContext(ctx, `
SELECT COUNT(*),
 COALESCE(SUM(CASE WHEN COALESCE(json_extract(n.payload,'$.record_id'),'')='' THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN COALESCE(json_extract(n.payload,'$.record_id'),'')='' AND COALESCE(json_extract(n.payload,'$.complete'),0)=1 THEN 1 ELSE 0 END),0)
FROM memory_tree_members m JOIN memory_tree_nodes n ON n.scope=m.scope AND n.id=m.id WHERE m.scope=?`, scope).Scan(&out.Nodes, &out.Branches, &out.Summarized); err != nil {
		return out, err
	}
	view, err := f.view(ctx, scope, root, true)
	if err != nil {
		return out, err
	}
	out.Root = &view
	return out, nil
}

// publishedNode loads a node only if it belongs to the published snapshot of the scope.
func (f *SummaryForest) publishedNode(ctx context.Context, scope, id string) (SummaryNode, error) {
	if f == nil || f.store == nil || strings.TrimSpace(scope) == "" || strings.TrimSpace(id) == "" {
		return SummaryNode{}, errors.New("memory tree unavailable")
	}
	var member int
	if err := f.store.db.QueryRowContext(ctx, "SELECT 1 FROM memory_tree_members WHERE scope=? AND id=?", scope, id).Scan(&member); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SummaryNode{}, fmt.Errorf("node is not in the current memory scope")
		}
		return SummaryNode{}, err
	}
	return f.node(ctx, scope, id)
}

// Peek returns one node of the published snapshot without its children.
func (f *SummaryForest) Peek(ctx context.Context, scope, id string) (TreeNodeView, error) {
	n, err := f.publishedNode(ctx, scope, id)
	if err != nil {
		return TreeNodeView{}, err
	}
	return f.view(ctx, scope, n, false)
}

// Subtree returns one node of the published snapshot with its direct children.
func (f *SummaryForest) Subtree(ctx context.Context, scope, id string) (TreeNodeView, error) {
	n, err := f.publishedNode(ctx, scope, id)
	if err != nil {
		return TreeNodeView{}, err
	}
	return f.view(ctx, scope, n, true)
}

// Locate returns the chain from the root down to the node, each element with its
// direct children, so the console can open a node deep in the tree in one call.
// The chain comes from the record routes the tree already keeps: the ancestors of
// any record below the node are the ancestors of the node.
func (f *SummaryForest) Locate(ctx context.Context, scope, id string) ([]TreeNodeView, error) {
	target, err := f.Subtree(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	leaf, err := f.node(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	for len(leaf.Children) > 0 {
		if leaf, err = f.node(ctx, scope, leaf.Children[0]); err != nil {
			return nil, err
		}
	}
	rows, err := f.store.db.QueryContext(ctx, `
SELECT r.node_id FROM memory_tree_routes r JOIN memory_tree_nodes n ON n.scope=r.scope AND n.id=r.node_id
WHERE r.scope=? AND r.record_id=? ORDER BY CAST(json_extract(n.payload,'$.level') AS INTEGER) DESC, r.node_id`, scope, leaf.RecordID)
	if err != nil {
		return nil, err
	}
	var chain []string
	for rows.Next() {
		var nodeID string
		if err = rows.Scan(&nodeID); err != nil {
			rows.Close()
			return nil, err
		}
		chain = append(chain, nodeID)
		if nodeID == id {
			break
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(chain) == 0 || chain[len(chain)-1] != id {
		return nil, fmt.Errorf("node is not in the current memory scope")
	}
	out := make([]TreeNodeView, 0, len(chain))
	for _, nodeID := range chain[:len(chain)-1] {
		v, e := f.Subtree(ctx, scope, nodeID)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return append(out, target), nil
}

func (f *SummaryForest) view(ctx context.Context, scope string, n SummaryNode, withChildren bool) (TreeNodeView, error) {
	v := TreeNodeView{ID: n.ID, Label: n.Label, Summary: n.Summary, Count: n.Count, Level: n.Level, Complete: n.Complete, RecordID: n.RecordID, ChildCount: len(n.Children)}
	if n.RecordID != "" {
		// A record may have been archived or deleted after the snapshot; the node still draws.
		if r, err := f.store.GetRecord(ctx, n.RecordID); err == nil {
			_, kind := recordDirectory(r)
			content := []rune(strings.TrimSpace(r.Content))
			if len(content) > treeViewRecordRunes {
				content = append(content[:treeViewRecordRunes], '…')
			}
			v.Record = &TreeRecordView{Modality: r.Modality, Scope: r.Scope, Kind: kind, Canonical: r.IsCanonical, Importance: r.Importance, CreatedAt: r.CreatedAt, Content: string(content)}
		}
		return v, nil
	}
	if !withChildren {
		return v, nil
	}
	for _, id := range n.Children {
		child, err := f.node(ctx, scope, id)
		if err != nil {
			return v, err
		}
		cv, err := f.view(ctx, scope, child, false)
		if err != nil {
			return v, err
		}
		v.Children = append(v.Children, cv)
	}
	return v, nil
}

// RebuildNow publishes the structural tree for the scope immediately, without
// calling a model, and lets the background worker continue with model-written
// abstracts. A manual request skips the worker's cool-down.
func (f *SummaryForest) RebuildNow(ctx context.Context, scope string) error {
	if f == nil || f.store == nil || strings.TrimSpace(scope) == "" {
		return errors.New("memory tree unavailable")
	}
	if err := f.Refresh(ctx, scope, nil); err != nil {
		return err
	}
	f.mu.Lock()
	delete(f.retry, scope)
	f.mu.Unlock()
	f.Request(ctx, scope)
	return nil
}

var frontierLocator = regexp.MustCompile(`\[node=(mt_[0-9a-f]+); n=(\d+)\]`)

// FrontierNodeIDs lists the tree nodes present in an injected memory prompt, in
// order. It reads the same locator the model reads, so the console highlights
// exactly what the model was given.
func FrontierNodeIDs(prompt string) []string {
	var ids []string
	seen := map[string]bool{}
	for _, m := range frontierLocator.FindAllStringSubmatch(prompt, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			ids = append(ids, m[1])
		}
	}
	return ids
}
