package sessions

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// seedConversation builds a session shaped like a used one: an explicit mode,
// a transcript whose rows share a second, and a context log in two scopes.
func seedConversation(t *testing.T, s *Store, id string) {
	t.Helper()
	ctx := context.Background()
	if err := s.EnsureSession(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE chat_sessions SET title = ? WHERE id = ?`, "重构方案", id); err != nil {
		t.Fatal(err)
	}
	for i, m := range []struct{ role, text, typ string }{
		{"user", "把配置读取拆出去", "text"},
		{"assistant", "好的，先看 config.go", "text"},
		{"tool", `{"ok":true}`, "tool_result"},
		{"assistant", "拆完了", "text"},
	} {
		var meta map[string]any
		if m.typ == "tool_result" {
			meta = map[string]any{"tool": "read_file", "n": i}
		}
		if err := s.AppendMessage(ctx, id, m.role, m.text, m.typ, meta); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveMode(ctx, id, ModeRecord{Profile: "coding", Canvas: "focused"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetExpert(ctx, id, "software-engineer"); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"eino-v1:aaaa", "eino-v1:bbbb"} {
		var batch []ContextEventRecord
		for _, n := range []string{"1", "2", "3"} {
			batch = append(batch, ContextEventRecord{ID: scope + "-ev" + n, Kind: "message", Payload: []byte(`{"scope":"` + scope + `","n":` + n + `}`)})
		}
		if err := s.AppendContextEvents(ctx, id, scope, batch, false); err != nil {
			t.Fatal(err)
		}
	}
}

func transcriptOf(t *testing.T, s *Store, id string) []Message {
	t.Helper()
	msgs, err := s.GetMessages(context.Background(), id, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func sessionRow(t *testing.T, s *Store, id string) (Session, bool) {
	t.Helper()
	list, err := s.List(context.Background(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range list {
		if x.ID == id {
			return x, true
		}
	}
	return Session{}, false
}

func countRows(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestForkSessionCopiesTranscriptEventsAndMode(t *testing.T) {
	s, _ := deletionStore(t)
	ctx := context.Background()
	seedConversation(t, s, "orig")

	res, err := s.ForkSession(ctx, " orig ", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Session.ID == "" || res.Session.ID == "orig" || !strings.HasPrefix(res.Session.ID, "sess_") {
		t.Fatalf("bad id %q", res.Session.ID)
	}
	if res.Messages != 4 || res.Events != 6 || !res.Mode {
		t.Fatalf("counts: %+v", res)
	}
	if res.Session.Title != "重构方案（分叉）" || res.Session.MessageCount != 4 {
		t.Fatalf("session: %+v", res.Session)
	}
	if res.ParentID != "orig" || res.ParentTitle != "重构方案" {
		t.Fatalf("lineage: %+v", res)
	}

	// Transcript: same rows in the same order, but every row is the fork's own.
	want, got := transcriptOf(t, s, "orig"), transcriptOf(t, s, res.Session.ID)
	if len(got) != len(want) {
		t.Fatalf("messages: %d vs %d", len(got), len(want))
	}
	seen := map[string]bool{}
	for _, m := range want {
		seen[m.ID] = true
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.SessionID != res.Session.ID {
			t.Fatalf("row %d belongs to %q", i, g.SessionID)
		}
		if seen[g.ID] || g.ID == "" || !strings.HasPrefix(g.ID, "msg_") {
			t.Fatalf("row %d reuses or lacks an id: %q", i, g.ID)
		}
		seen[g.ID] = true
		if g.Role != w.Role || g.Content != w.Content || g.Type != w.Type || g.MetaJSON != w.MetaJSON || g.CreatedAt != w.CreatedAt {
			t.Fatalf("row %d differs:\n got %+v\nwant %+v", i, g, w)
		}
	}

	// Context log: every scope, byte for byte, in sequence order.
	for _, scope := range []string{"eino-v1:aaaa", "eino-v1:bbbb"} {
		a, _, err := s.LoadContextEvents(ctx, "orig", scope, "", false, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		b, _, err := s.LoadContextEvents(ctx, res.Session.ID, scope, "", false, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(a) != 3 || len(b) != 3 {
			t.Fatalf("%s: %d vs %d events", scope, len(a), len(b))
		}
		for i := range a {
			if a[i].ID != b[i].ID || a[i].Kind != b[i].Kind || !bytes.Equal(a[i].Payload, b[i].Payload) {
				t.Fatalf("%s[%d] differs: %+v vs %+v", scope, i, a[i], b[i])
			}
		}
	}

	// Mode and lineage ride along.
	_, _, modes, err := s.LoadModes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m := modes[res.Session.ID]; m.Profile != "coding" || m.Canvas != "focused" {
		t.Fatalf("mode: %+v", modes)
	}
	row, ok := sessionRow(t, s, res.Session.ID)
	if !ok || row.ParentID != "orig" || row.ParentTitle != "重构方案" || row.ParentGone || row.MessageCount != 4 {
		t.Fatalf("listed fork: %+v ok=%v", row, ok)
	}
	// The copy keeps working as the same expert, and rebinding one leaves the other alone.
	if res.Session.ExpertID != "software-engineer" || row.ExpertID != "software-engineer" {
		t.Fatalf("expert on the copy: result=%q listed=%q", res.Session.ExpertID, row.ExpertID)
	}
	if err := s.SetExpert(ctx, res.Session.ID, "bug-hunter"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Expert(ctx, "orig"); got != "software-engineer" {
		t.Fatalf("rebinding the copy changed the original: %q", got)
	}
	if orig, _ := sessionRow(t, s, "orig"); orig.ParentID != "" || orig.ParentGone {
		t.Fatalf("the original grew a parent: %+v", orig)
	}
}

func TestForkSessionKeepsReadingOrder(t *testing.T) {
	s, _ := deletionStore(t)
	ctx := context.Background()
	if err := s.EnsureSession(ctx, "orig"); err != nil {
		t.Fatal(err)
	}
	// Row ids and timestamps disagree: the reading order is created_at, then rowid.
	for _, m := range []struct {
		text string
		at   int64
	}{{"late", 200}, {"early", 100}, {"early too", 100}, {"middle", 150}} {
		if _, err := s.db.Exec(`INSERT INTO chat_messages (id, session_id, role, content, msg_type, meta_json, created_at) VALUES (?, 'orig', 'user', ?, 'text', '', ?)`, "m-"+m.text, m.text, m.at); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.ForkSession(ctx, "orig", "")
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, m := range transcriptOf(t, s, res.Session.ID) {
		order = append(order, m.Content)
	}
	if got := strings.Join(order, ","); got != "early,early too,middle,late" {
		t.Fatalf("order after fork: %s", got)
	}
}

func TestForkSessionOfAnEmptyConversation(t *testing.T) {
	s, _ := deletionStore(t)
	ctx := context.Background()
	if err := s.EnsureSession(ctx, "blank"); err != nil {
		t.Fatal(err)
	}
	res, err := s.ForkSession(ctx, "blank", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Messages != 0 || res.Events != 0 || res.Mode {
		t.Fatalf("copied nothing yet: %+v", res)
	}
	if res.Session.Title != "新对话（分叉）" {
		t.Fatalf("title: %q", res.Session.Title)
	}
	if countRows(t, s, `SELECT COUNT(*) FROM session_modes WHERE session_id = ?`, res.Session.ID) != 0 {
		t.Fatal("a mode appeared from nowhere")
	}
}

func TestForkSessionTitles(t *testing.T) {
	s, _ := deletionStore(t)
	ctx := context.Background()
	seedConversation(t, s, "orig")

	titles := func(n int) []string {
		var out []string
		for i := 0; i < n; i++ {
			res, err := s.ForkSession(ctx, "orig", "")
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, res.Session.Title)
		}
		return out
	}
	if got := strings.Join(titles(3), "|"); got != "重构方案（分叉）|重构方案（分叉 2）|重构方案（分叉 3）" {
		t.Fatalf("numbering: %s", got)
	}

	// Forking a fork starts from the same base instead of stacking markers.
	row, _ := sessionRow(t, s, "orig")
	if row.Title != "重构方案" {
		t.Fatalf("original renamed: %q", row.Title)
	}
	list, _ := s.List(ctx, 100)
	var fork string
	for _, x := range list {
		if x.Title == "重构方案（分叉 2）" {
			fork = x.ID
		}
	}
	res, err := s.ForkSession(ctx, fork, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Session.Title != "重构方案（分叉 4）" {
		t.Fatalf("fork of a fork: %q", res.Session.Title)
	}
	if res.ParentTitle != "重构方案（分叉 2）" {
		t.Fatalf("lineage names the immediate parent: %q", res.ParentTitle)
	}

	// An explicit title is taken as typed, trimmed and bounded.
	res, err = s.ForkSession(ctx, "orig", "  试试另一条路  ")
	if err != nil || res.Session.Title != "试试另一条路" {
		t.Fatalf("explicit: %+v %v", res.Session, err)
	}
	res, err = s.ForkSession(ctx, "orig", strings.Repeat("长", 500))
	if err != nil || len([]rune(res.Session.Title)) != MaxTitleRunes {
		t.Fatalf("title not bounded to what the rename box holds: %d runes, %v", len([]rune(res.Session.Title)), err)
	}
	// Like a rename, a typed title is one line.
	res, err = s.ForkSession(ctx, "orig", "第一行\n  第二行")
	if err != nil || res.Session.Title != "第一行 第二行" {
		t.Fatalf("multi-line title: %q %v", res.Session.Title, err)
	}

	// A long original title is shortened before the marker goes on.
	if _, err := s.db.Exec(`UPDATE chat_sessions SET title = ? WHERE id = 'orig'`, strings.Repeat("长", 200)); err != nil {
		t.Fatal(err)
	}
	res, err = s.ForkSession(ctx, "orig", "")
	if err != nil || !strings.HasSuffix(res.Session.Title, "（分叉）") || len([]rune(res.Session.Title)) > forkBaseMaxRunes+len([]rune("…（分叉）")) {
		t.Fatalf("long title: %q %v", res.Session.Title, err)
	}
}

func TestForkSessionIsIndependentOfTheOriginal(t *testing.T) {
	s, _ := deletionStore(t)
	ctx := context.Background()
	seedConversation(t, s, "orig")
	res, err := s.ForkSession(ctx, "orig", "")
	if err != nil {
		t.Fatal(err)
	}
	fork := res.Session.ID

	// Writes on either side stay on that side.
	if err := s.AppendMessage(ctx, fork, "user", "only in the fork", "text", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(ctx, "orig", "user", "only in the original", "text", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendContextEvents(ctx, fork, "eino-v1:aaaa", []ContextEventRecord{{ID: "fork-only", Kind: "message", Payload: []byte("x")}}, false); err != nil {
		t.Fatal(err)
	}
	// The same event id may exist in both logs; uniqueness is per session.
	if err := s.AppendContextEvents(ctx, "orig", "eino-v1:aaaa", []ContextEventRecord{{ID: "fork-only", Kind: "message", Payload: []byte("y")}}, false); err != nil {
		t.Fatal(err)
	}
	if got := len(transcriptOf(t, s, fork)); got != 5 {
		t.Fatalf("fork has %d messages", got)
	}
	if got := len(transcriptOf(t, s, "orig")); got != 5 {
		t.Fatalf("original has %d messages", got)
	}
	if !contains(transcriptOf(t, s, fork), "only in the fork") || contains(transcriptOf(t, s, fork), "only in the original") {
		t.Fatal("transcripts leaked into each other")
	}

	// Deleting the original leaves a complete, self-standing fork.
	if err := s.Delete(ctx, "orig"); err != nil {
		t.Fatal(err)
	}
	if got := len(transcriptOf(t, s, fork)); got != 5 {
		t.Fatalf("fork lost messages: %d", got)
	}
	if countRows(t, s, `SELECT COUNT(*) FROM chat_context_events WHERE session_id = ?`, fork) != 7 {
		t.Fatal("fork lost context events")
	}
	row, ok := sessionRow(t, s, fork)
	if !ok || !row.ParentGone || row.ParentID != "orig" || row.ParentTitle != "重构方案" {
		t.Fatalf("lineage after deleting the original: %+v", row)
	}
	// And the other direction: deleting the fork removes its lineage row only.
	if err := s.Delete(ctx, fork); err != nil {
		t.Fatal(err)
	}
	if countRows(t, s, `SELECT COUNT(*) FROM chat_session_forks`) != 0 {
		t.Fatal("lineage outlived the fork")
	}
}

func contains(msgs []Message, text string) bool {
	for _, m := range msgs {
		if m.Content == text {
			return true
		}
	}
	return false
}

func TestForkSessionShowsTheOriginalsCurrentTitle(t *testing.T) {
	s, _ := deletionStore(t)
	ctx := context.Background()
	seedConversation(t, s, "orig")
	res, err := s.ForkSession(ctx, "orig", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE chat_sessions SET title = '改名之后' WHERE id = 'orig'`); err != nil {
		t.Fatal(err)
	}
	if row, _ := sessionRow(t, s, res.Session.ID); row.ParentTitle != "改名之后" || row.ParentGone {
		t.Fatalf("renamed original: %+v", row)
	}
	if err := s.Delete(ctx, "orig"); err != nil {
		t.Fatal(err)
	}
	// Gone: the title recorded at fork time is the only one left.
	if row, _ := sessionRow(t, s, res.Session.ID); row.ParentTitle != "重构方案" || !row.ParentGone {
		t.Fatalf("deleted original: %+v", row)
	}
}

func TestForkSessionIsAllOrNothing(t *testing.T) {
	s, db := deletionStore(t)
	ctx := context.Background()
	seedConversation(t, s, "orig")
	if _, err := db.Exec("CREATE TRIGGER reject_copy BEFORE INSERT ON chat_session_forks BEGIN SELECT RAISE(ABORT,'injected storage failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ForkSession(ctx, "orig", ""); err == nil {
		t.Fatal("a failed fork reported success")
	}
	// The failure came last, after the transcript, events and mode were copied.
	for table, want := range map[string]int{
		"chat_sessions": 1, "chat_messages": 4, "chat_context_events": 6, "session_modes": 1, "chat_session_experts": 1, "chat_session_forks": 0,
	} {
		if got := countRows(t, s, "SELECT COUNT(*) FROM "+table); got != want {
			t.Fatalf("%s: %d rows after a failed fork, want %d", table, got, want)
		}
	}
	if _, err := db.Exec("DROP TRIGGER reject_copy"); err != nil {
		t.Fatal(err)
	}
	res, err := s.ForkSession(ctx, "orig", "")
	if err != nil {
		t.Fatalf("retry after the failure: %v", err)
	}
	if res.Messages != 4 || res.Events != 6 {
		t.Fatalf("retry: %+v", res)
	}
}

func TestForkSessionRejectsMissingSessions(t *testing.T) {
	s, _ := deletionStore(t)
	ctx := context.Background()
	if _, err := s.ForkSession(ctx, "   ", ""); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("empty id: %v", err)
	}
	if _, err := s.ForkSession(ctx, "nope", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if countRows(t, s, `SELECT COUNT(*) FROM chat_sessions`) != 0 {
		t.Fatal("a session appeared from a failed fork")
	}
}

func TestForkLineageIsRemovedWithItsSession(t *testing.T) {
	// The delete paths share one table list; a fork must not survive any of them.
	for _, name := range []string{"one", "batch", "others", "title"} {
		t.Run(name, func(t *testing.T) {
			s, _ := deletionStore(t)
			ctx := context.Background()
			seedConversation(t, s, "orig")
			res, err := s.ForkSession(ctx, "orig", "分叉用例")
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "one":
				err = s.Delete(ctx, res.Session.ID)
			case "batch":
				_, err = s.DeleteBatch(ctx, []string{res.Session.ID})
			case "others":
				_, err = s.DeleteAllExcept(ctx, "orig")
			case "title":
				_, err = s.DeleteByTitle(ctx, "分叉用例", "")
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := countRows(t, s, `SELECT COUNT(*) FROM chat_session_forks`); got != 0 {
				t.Fatalf("lineage rows left: %d", got)
			}
			if got := countRows(t, s, `SELECT COUNT(*) FROM chat_sessions WHERE id = 'orig'`); got != 1 {
				t.Fatal("the original went with it")
			}
		})
	}
}
