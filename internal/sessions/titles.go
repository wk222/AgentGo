package sessions

import (
	"context"
	"strings"
)

// titlesChunk keeps one query well under SQLite's bound-variable limit.
const titlesChunk = 200

// Titles returns the titles of the given sessions, keyed by id. An id that has no
// row (an internal session, or one deleted a moment ago) is simply absent, so the
// caller decides what to call it. Unlike List it is not limited to the most
// recent sessions: a run that is going on right now may belong to an old one.
func (s *Store) Titles(ctx context.Context, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	seen := make(map[string]struct{}, len(ids))
	uniq := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}
	for start := 0; start < len(uniq); start += titlesChunk {
		chunk := uniq[start:min(start+titlesChunk, len(uniq))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx,
			`SELECT id, title FROM chat_sessions WHERE id IN (?`+strings.Repeat(",?", len(chunk)-1)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, title string
			if err := rows.Scan(&id, &title); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = title
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
