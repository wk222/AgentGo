package memory

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// BrowseTimeline is an extractive temporal tree. Interval nodes hold counts and
// stable paths, never lossy replacements of the original evidence.
func (s *SQLiteStore) BrowseTimeline(ctx context.Context, scope, node string, offset, limit int) (DirectoryPage, error) {
	page := DirectoryPage{Nodes: []DirectoryNode{}}
	if scope == "" || offset < 0 || offset > 100000 {
		return page, fmt.Errorf("invalid memory timeline scope or offset")
	}
	limit = max(1, min(8, limit))
	part := strings.TrimPrefix(node, "time:")
	if node != "" && !strings.HasPrefix(node, "time:") {
		return page, fmt.Errorf("invalid time node")
	}
	layout := ""
	if len(part) == 4 {
		layout = "2006"
	} else if len(part) == 7 {
		layout = "2006-01"
	} else if part != "" {
		return page, fmt.Errorf("invalid time interval")
	}
	where := " FROM memories WHERE " + visibleMemory + " AND scope IN (?, 'global')"
	args := []any{scope}
	if layout != "" {
		start, err := time.Parse(layout, part)
		if err != nil || start.Year() < 1970 || start.Year() > 9998 {
			return page, fmt.Errorf("invalid time interval")
		}
		end := start.AddDate(1, 0, 0)
		if layout == "2006-01" {
			end = start.AddDate(0, 1, 0)
		}
		where += " AND created_at>=? AND created_at<?"
		args = append(args, start.Unix(), end.Unix())
	}
	format := "%Y"
	if layout == "2006" {
		format = "%Y-%m"
	}
	query := "SELECT strftime('" + format + "',created_at,'unixepoch'),COUNT(*)" + where + " GROUP BY 1 ORDER BY 1 DESC"
	if layout == "2006-01" {
		query = "SELECT id,substr(content,1,160),scope" + where + " ORDER BY created_at DESC,id"
	}
	query += " LIMIT ? OFFSET ?"
	args = append(args, limit+1, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		n := DirectoryNode{}
		if layout == "2006-01" {
			if err = rows.Scan(&n.ID, &n.Label, &n.Scope); err != nil {
				return page, err
			}
			n.Type = "record"
		} else {
			if err = rows.Scan(&n.Label, &n.Count); err != nil {
				return page, err
			}
			n.ID = "time:" + n.Label
			n.Type = "timeline"
		}
		if len(page.Nodes) == limit {
			page.NextOffset = offset + limit
			break
		}
		page.Nodes = append(page.Nodes, n)
	}
	return page, rows.Err()
}
