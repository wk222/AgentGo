package memory

import (
	"agentgo/internal/config"
	"agentgo/internal/promptbudget"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Directories are live read projections of authoritative records. They are
// rebuilt by SQL, never a recursively summarized or separately authoritative tree.
const validMetadata = "(CASE WHEN json_valid(metadata) THEN metadata ELSE '{}' END)"
const topicExpression = "(CASE WHEN json_type(" + validMetadata + ",'$.topic')='text' AND length(json_extract(" + validMetadata + ",'$.topic')) BETWEEN 1 AND 80 THEN json_extract(" + validMetadata + ",'$.topic') ELSE COALESCE(NULLIF(modality,''),'other') END)"
const kindExpression = "(CASE WHEN json_type(" + validMetadata + ",'$.memory_kind')='text' AND length(json_extract(" + validMetadata + ",'$.memory_kind')) BETWEEN 1 AND 40 THEN json_extract(" + validMetadata + ",'$.memory_kind') ELSE COALESCE(NULLIF(modality,''),'other') END)"
const visibleMemory = "status='active' AND COALESCE(supersedes_id,'')='' AND COALESCE(contradicted_by,'')='' AND COALESCE(json_extract(" + validMetadata + ",'$.atomic_container'),0)!=1 AND COALESCE(json_extract(" + validMetadata + ",'$.atomic_children'),0)!=1"

type DirectoryNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Type  string `json:"type"`
	Count int    `json:"count,omitempty"`
	Scope string `json:"scope,omitempty"`
}
type DirectoryPage struct {
	Nodes      []DirectoryNode `json:"nodes"`
	NextOffset int             `json:"next_offset,omitempty"`
}

func DirectoryPath(topic, kind string) string {
	path := "t:" + base64.RawURLEncoding.EncodeToString([]byte(topic))
	if kind != "" {
		path += "/k:" + base64.RawURLEncoding.EncodeToString([]byte(kind))
	}
	return path
}
func parseDirectoryPath(path string) (topic, kind string, err error) {
	if path == "" {
		return
	}
	if len(path) > 700 {
		return "", "", fmt.Errorf("memory directory path too long")
	}
	parts := strings.Split(path, "/")
	if len(parts) > 2 || !strings.HasPrefix(parts[0], "t:") {
		return "", "", fmt.Errorf("invalid memory directory")
	}
	decode := func(s string, maxLen int) (string, error) {
		b, e := base64.RawURLEncoding.DecodeString(s)
		if e != nil || !utf8.Valid(b) || len([]rune(string(b))) > maxLen || len(b) == 0 {
			return "", fmt.Errorf("invalid directory component")
		}
		return string(b), nil
	}
	topic, err = decode(parts[0][2:], 80)
	if err != nil {
		return
	}
	if len(parts) == 2 {
		if !strings.HasPrefix(parts[1], "k:") {
			return "", "", fmt.Errorf("invalid memory directory")
		}
		kind, err = decode(parts[1][2:], 40)
	}
	return
}
func recordDirectory(r Record) (topic, kind string) {
	topic, _ = r.Metadata["topic"].(string)
	kind, _ = r.Metadata["memory_kind"].(string)
	if len([]rune(topic)) == 0 || len([]rune(topic)) > 80 {
		topic = r.Modality
	}
	if len([]rune(kind)) == 0 || len([]rune(kind)) > 40 {
		kind = r.Modality
	}
	if topic == "" {
		topic = "other"
	}
	if kind == "" {
		kind = "other"
	}
	return
}

// BrowseDirectory can select only the supplied conversation and global records.
// Nodes contain no source text until a record is explicitly expanded.
func (s *SQLiteStore) BrowseDirectory(ctx context.Context, scope, path string, offset, limit int) (DirectoryPage, error) {
	page := DirectoryPage{Nodes: []DirectoryNode{}}
	if scope == "" {
		return page, fmt.Errorf("memory scope is required")
	}
	if offset < 0 || offset > 100000 {
		return page, fmt.Errorf("invalid directory offset")
	}
	limit = max(1, min(8, limit))
	topic, kind, err := parseDirectoryPath(path)
	if err != nil {
		return page, err
	}
	where := " FROM memories m WHERE " + visibleMemory + " AND scope IN (?, 'global')"
	args := []any{scope}
	if topic != "" {
		where += " AND " + topicExpression + "=?"
		args = append(args, topic)
	}
	if kind != "" {
		where += " AND " + kindExpression + "=?"
		args = append(args, kind)
	}
	var query string
	if topic == "" {
		query = "SELECT " + topicExpression + ",COUNT(*)" + where + " GROUP BY " + topicExpression + " ORDER BY 1"
	}
	if topic != "" && kind == "" {
		query = "SELECT " + kindExpression + ",COUNT(*)" + where + " GROUP BY " + kindExpression + " ORDER BY 1"
	}
	if kind != "" {
		query = "SELECT id,substr(content,1,160),scope" + where + " ORDER BY is_canonical DESC,updated_at DESC,id"
	}
	query += " LIMIT ? OFFSET ?"
	args = append(args, limit+1, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var n DirectoryNode
		if kind != "" {
			if err = rows.Scan(&n.ID, &n.Label, &n.Scope); err != nil {
				return page, err
			}
			n.Type = "record"
		} else {
			var label string
			if err = rows.Scan(&label, &n.Count); err != nil {
				return page, err
			}
			n.Label = label
			n.Type = "directory"
			if topic == "" {
				n.ID = DirectoryPath(label, "")
			} else {
				n.ID = DirectoryPath(topic, label)
			}
		}
		if len(page.Nodes) == limit {
			page.NextOffset = offset + limit
			break
		}
		page.Nodes = append(page.Nodes, n)
	}
	return page, rows.Err()
}

// PromptContext shows a bounded task-relevant directory and source excerpts.
// Query-based retrieval still selects records directly, so a bad topic assignment
// cannot hide an exact keyword match behind a directory route.
func PromptContext(ctx context.Context, records []Record, budget int) string {
	if budget <= 0 {
		return ""
	}
	if !config.MemoryHierarchy.On() || !automaticRecall(ctx) {
		return packContextRecords(records, budget)
	}
	var catalog strings.Builder
	seen := map[string]bool{}
	for _, r := range records {
		if r.Status != "active" && r.Status != "" || r.SupersedesID != "" || r.ContradictedBy != "" || r.Metadata["atomic_container"] == true || r.Metadata["atomic_children"] == true {
			continue
		}
		topic, kind := recordDirectory(r)
		node := DirectoryPath(topic, kind)
		if seen[node] {
			continue
		}
		line := "Directory " + topic + " / " + kind + "; node=" + node + "\n"
		if promptbudget.Estimate(catalog.String()+line) > min(300, budget/4) {
			continue
		}
		seen[node] = true
		catalog.WriteString(line)
	}
	body := catalog.String()
	if body != "" {
		body = "Memory directory (historical references):\n" + body + "Use browse_memory to expand a node, search exact keywords, or read a record/source ID.\n"
	}
	if promptbudget.Estimate(body) > budget/2 {
		body = ""
	}
	detail := packContextRecords(records, budget-promptbudget.Estimate(body)-1)
	return strings.TrimSpace(body + detail)
}
