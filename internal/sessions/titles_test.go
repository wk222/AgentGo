package sessions

import (
	"context"
	"fmt"
	"testing"
)

func TestTitlesFindsOnlyWhatExists(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for id, title := range map[string]string{"a": "写周报", "b": "重构方案"} {
		if err := s.EnsureSession(ctx, id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE chat_sessions SET title = ? WHERE id = ?`, title, id); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.Titles(ctx, []string{"a", " b ", "a", "", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["a"] != "写周报" || got["b"] != "重构方案" {
		t.Fatalf("titles = %v", got)
	}
	if empty, err := s.Titles(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("no ids: %v, %v", empty, err)
	}
}

// A run that is going on right now can belong to an old conversation, so the lookup
// is by id and must not depend on how many sessions there are, or on how it is
// split into queries.
func TestTitlesReachesPastTheRecentList(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	const n = titlesChunk*2 + 7
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("s%04d", i)
		ids = append(ids, id)
		if err := s.EnsureSession(ctx, id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE chat_sessions SET title = ? WHERE id = ?`, "t-"+id, id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Titles(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n {
		t.Fatalf("got %d titles for %d sessions", len(got), n)
	}
	for _, id := range []string{ids[0], ids[titlesChunk-1], ids[titlesChunk], ids[n-1]} {
		if got[id] != "t-"+id {
			t.Fatalf("title of %s = %q", id, got[id])
		}
	}
}
