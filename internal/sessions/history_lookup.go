package sessions

import (
	"context"
	"database/sql"
	"errors"
)

const historyColumns = "id,session_id,role,COALESCE(content,''),COALESCE(msg_type,'text'),COALESCE(meta_json,''),COALESCE(created_at,0)"

func (s *Store) ReadHistoryMessage(ctx context.Context, sid, id string) (Message, error) {
	var m Message
	err := s.db.QueryRowContext(ctx, "SELECT "+historyColumns+" FROM chat_messages WHERE session_id=? AND id=?", sid, id).Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.Type, &m.MetaJSON, &m.CreatedAt)
	return m, err
}

// SearchHistory searches the full UI transcript, newest first. IDs and cursors
// are always resolved within sid; SQL arguments never select another session.
func (s *Store) SearchHistory(ctx context.Context, sid, query, before string, limit int) ([]Message, string, error) {
	limit = min(8, max(1, limit))
	args := []any{sid}
	predicate := "session_id=? AND role IN ('user','assistant') AND COALESCE(msg_type,'text') IN ('','text')"
	if before != "" {
		var rowid int64
		if err := s.db.QueryRowContext(ctx, "SELECT rowid FROM chat_messages WHERE session_id=? AND id=?", sid, before).Scan(&rowid); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, "", ErrContextCursor
			}
			return nil, "", err
		}
		predicate += " AND rowid<?"
		args = append(args, rowid)
	}
	if query != "" {
		predicate += " AND instr(lower(COALESCE(content,'')),lower(?))>0"
		args = append(args, query)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, "SELECT "+historyColumns+" FROM chat_messages WHERE "+predicate+" ORDER BY rowid DESC LIMIT ?", args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.Type, &m.MetaJSON, &m.CreatedAt); err != nil {
			return nil, "", err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = out[len(out)-1].ID
	}
	return out, next, nil
}
