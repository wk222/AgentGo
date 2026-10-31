package bridge

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentdb "agentgo/internal/db"
	"agentgo/internal/sessions"
)

func sessionService(t *testing.T) (*AppService, *sessions.Store, string) {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := agentdb.Configure(conn); err != nil {
		t.Fatal(err)
	}
	store, err := sessions.Open(conn)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	return &AppService{rt: &Runtime{dataDir: dir, sessions: store}}, store, dir
}

func TestRenameSessionStoresTheCleanedTitle(t *testing.T) {
	svc, store, _ := sessionService(t)
	sess, _ := store.Create(t.Context(), "")

	res := svc.RenameSession(sess.ID, "  销售周报\n分析 ")
	if res["success"] != true || res["title"] != "销售周报 分析" || res["id"] != sess.ID {
		t.Fatalf("answer: %v", res)
	}
	listed := svc.ListSessions()
	if len(listed) != 1 || listed[0]["title"] != "销售周报 分析" {
		t.Fatalf("the sidebar list does not show the new title: %v", listed)
	}

	for _, c := range []struct{ id, title, wantErr string }{
		{sess.ID, "   ", "标题不能为空"},
		{"sess_gone", "x", "这条对话已经不存在"},
		{"  ", "x", "empty session_id"},
	} {
		got := svc.RenameSession(c.id, c.title)
		if got["success"] != false || got["error"] != c.wantErr {
			t.Errorf("RenameSession(%q, %q) = %v, want error %q", c.id, c.title, got, c.wantErr)
		}
	}
}

func TestExportSessionWritesTheConversationAsMarkdown(t *testing.T) {
	svc, store, dir := sessionService(t)
	ctx := t.Context()
	sess, _ := store.Create(ctx, "销售周报: 分析?")
	_ = store.AppendMessage(ctx, sess.ID, "user", "请汇总本周销量", "text", nil)
	_ = store.AppendMessage(ctx, sess.ID, "assistant", "济南 12，青岛 9。", "text", nil)
	_ = store.AppendMessage(ctx, sess.ID, "assistant", "", "approval", map[string]any{"tool_name": "replace_workspace_text", "arguments": `{"path":"C:\\secret\\a.xlsx"}`})
	other, _ := store.Create(ctx, "other")
	_ = store.AppendMessage(ctx, other.ID, "user", "ANOTHER-CONVERSATION", "text", nil)

	res := svc.ExportSession(sess.ID)
	if res["success"] != true {
		t.Fatalf("export failed: %v", res)
	}
	name := res["name"].(string)
	if !strings.HasPrefix(name, sessionExportPrefix) || !strings.HasSuffix(name, ".md") || !strings.Contains(name, sess.ID) {
		t.Fatalf("name %q", name)
	}
	if res["url"] != "/attachments/"+name || res["messages"] != 3 || res["size"].(int64) <= 0 {
		t.Fatalf("answer: %v", res)
	}
	// What the browser saves the file as: the title, safe for Windows, with the day.
	if want := "销售周报_ 分析_-" + time.Now().Format("20060102") + ".md"; res["filename"] != want {
		t.Fatalf("filename %q, want %q", res["filename"], want)
	}
	if want := filepath.Join(dir, "attachments", name); res["path"] != want {
		t.Fatalf("path %v, want %v", res["path"], want)
	}

	data, err := os.ReadFile(res["path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "\xEF\xBB\xBF# ") {
		t.Fatalf("the file should start with a UTF-8 byte order mark, then the heading: %q", string(data[:12]))
	}
	text := string(data)
	for _, want := range []string{"# 销售周报: 分析?", "请汇总本周销量", "济南 12，青岛 9。", "请求批准：replace_workspace_text"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	for _, leaked := range []string{"ANOTHER-CONVERSATION", `C:\\secret`, "更早的消息"} {
		if strings.Contains(text, leaked) {
			t.Errorf("the export must not contain %q:\n%s", leaked, text)
		}
	}
}

func TestExportSessionRefusesWhatItCannotExport(t *testing.T) {
	svc, _, _ := sessionService(t)
	if got := svc.ExportSession("sess_gone"); got["success"] != false || got["error"] != "这条对话已经不存在" {
		t.Fatalf("unknown id: %v", got)
	}
	if got := svc.ExportSession(" "); got["success"] != false || got["error"] != "empty session_id" {
		t.Fatalf("empty id: %v", got)
	}
	if got := (&AppService{rt: &Runtime{}}).ExportSession("sess_a"); got["success"] != false {
		t.Fatalf("no data dir: %v", got)
	}
}

func TestSessionExportNamesAreSafeURLPathsThatSortByTime(t *testing.T) {
	at := time.Date(2026, 10, 28, 9, 5, 7, 0, time.Local)
	if got, want := sessionExportName("sess_ab12-CD", at), "agentgo-chat-20261028-090507-sess_ab12-CD.md"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := sessionExportName(`..\..\evil/名字 .md`, at); strings.ContainsAny(got, `\/: 名字`) || strings.Contains(got, "..") {
		t.Fatalf("unsafe name %q", got)
	}
	if a, b := sessionExportName("s", at), sessionExportName("s", at.Add(time.Second)); a >= b {
		t.Fatalf("names must sort chronologically: %q !< %q", a, b)
	}
}

func TestWriteSessionExportKeepsOnlyTheNewestAndNeverTouchesOtherFiles(t *testing.T) {
	dir := t.TempDir()
	foreign := []string{"agentgo-diagnostics-20260101-000000.zip", "chat-notes.md", "report.xlsx", "agentgo-chat-readme.txt"}
	for _, n := range foreign {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("user file"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, sessionExportPrefix+"20200101-000000-crashed.md.tmp"), []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	for i := 0; i < sessionExportKeep+5; i++ {
		name := sessionExportName(fmt.Sprintf("s%02d", i), base.Add(time.Duration(i)*time.Minute))
		if _, _, err := writeSessionExport(dir, name, "# x\n"); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	var exports []string
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Name(), ".tmp"):
			t.Errorf("a leftover half-written export survived: %s", e.Name())
		case strings.HasPrefix(e.Name(), sessionExportPrefix) && strings.HasSuffix(e.Name(), ".md"):
			exports = append(exports, e.Name())
		}
	}
	if len(exports) != sessionExportKeep {
		t.Fatalf("kept %d exports, want %d", len(exports), sessionExportKeep)
	}
	oldest := sessionExportName("s05", base.Add(5*time.Minute))
	newest := sessionExportName("s34", base.Add(34*time.Minute))
	has := func(n string) bool { _, err := os.Stat(filepath.Join(dir, n)); return err == nil }
	if has(sessionExportName("s04", base.Add(4*time.Minute))) || !has(oldest) || !has(newest) {
		t.Fatalf("the wrong exports were pruned")
	}
	for _, n := range foreign {
		if !has(n) {
			t.Errorf("pruning removed a file that is not an export: %s", n)
		}
	}
}
