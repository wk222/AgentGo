package sessions

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	agentdb "agentgo/internal/db"
)

func exportStore(t *testing.T) *Store {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := agentdb.Configure(conn); err != nil {
		t.Fatal(err)
	}
	store, err := Open(conn)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestRenameKeepsTheTitleTheUserChoseAndTheListOrder(t *testing.T) {
	ctx := context.Background()
	store := exportStore(t)
	sess, err := store.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE chat_sessions SET updated_at = 1000 WHERE id = ?`, sess.ID); err != nil {
		t.Fatal(err)
	}

	got, err := store.Rename(ctx, sess.ID, "  销售周报\n分析  ")
	if err != nil || got != "销售周报 分析" {
		t.Fatalf("Rename = %q, %v", got, err)
	}
	read, err := store.Get(ctx, sess.ID)
	if err != nil || read.Title != "销售周报 分析" {
		t.Fatalf("stored title %q, %v", read.Title, err)
	}
	if read.UpdatedAt != 1000 {
		t.Fatalf("renaming is no activity, but updated_at moved to %d", read.UpdatedAt)
	}

	// The first user message names a session that still has its default title, never one the user chose.
	if err := store.AutoTitleFromUserMessage(ctx, sess.ID, "帮我看看这个表"); err != nil {
		t.Fatal(err)
	}
	if read, _ = store.Get(ctx, sess.ID); read.Title != "销售周报 分析" {
		t.Fatalf("auto-titling overwrote the chosen title: %q", read.Title)
	}
}

func TestRenameRefusesWhatCannotBeATitle(t *testing.T) {
	ctx := context.Background()
	store := exportStore(t)
	sess, _ := store.Create(ctx, "keep")

	if _, err := store.Rename(ctx, sess.ID, " \n\t "); err == nil {
		t.Fatal("a blank title must be refused")
	}
	if _, err := store.Rename(ctx, "   ", "x"); err == nil {
		t.Fatal("an empty id must be refused")
	}
	if _, err := store.Rename(ctx, "sess_missing", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if read, _ := store.Get(ctx, sess.ID); read.Title != "keep" {
		t.Fatalf("a refused rename changed the title to %q", read.Title)
	}

	long := strings.Repeat("长", 200)
	got, err := store.Rename(ctx, sess.ID, long)
	if err != nil || len([]rune(got)) != MaxTitleRunes {
		t.Fatalf("a long title should be cut to %d runes, got %d (%v)", MaxTitleRunes, len([]rune(got)), err)
	}
}

func TestGetOfAnUnknownSessionIsNotFound(t *testing.T) {
	if _, err := exportStore(t).Get(context.Background(), "sess_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestMarkdownTellsWhatWasSaidAndLeavesOutWhatWasNot(t *testing.T) {
	at := time.Date(2026, 10, 27, 22, 10, 0, 0, time.Local).Unix()
	sess := Session{ID: "sess_a", Title: "销售周报 分析"}
	msgs := []Message{
		{Role: "user", Type: "text", Content: "请汇总 **本周** 销量", CreatedAt: at, MetaJSON: `{"images":["a.png","b.png"]}`},
		{Role: "assistant", Type: "text", Content: "已汇总：\n\n| 区域 | 销量 |\n|---|---|\n| 济南 | 12 |", CreatedAt: at + 60},
		{Role: "assistant", Type: "approval", MetaJSON: `{"tool_name":"write_workspace_file","arguments":"{\"command\":\"del /f secret.xlsx\"}","status":"pending"}`, CreatedAt: at + 61},
		{Role: "assistant", Type: "question", Content: "要覆盖原文件吗？\n(是/否)", CreatedAt: at + 62},
		{Role: "assistant", Type: "aui", Content: "", MetaJSON: `{"component":"Chart"}`, CreatedAt: at + 63},
		{Role: "system", Type: "text", Content: "⏰ 事件触发", CreatedAt: at + 64},
		{Role: "assistant", Type: "text", Content: "   ", CreatedAt: at + 65},                  // nothing to show
		{Role: "assistant", Type: "reasoning", Content: "secret thoughts", CreatedAt: at + 66}, // never part of the record
	}
	got := Markdown(sess, msgs, time.Date(2026, 10, 28, 0, 5, 0, 0, time.Local), false)

	for _, want := range []string{
		"# 销售周报 分析\n",
		"> 导出自 AgentGo · 2026-10-28 00:05 · 6 条消息\n",
		"**🧑 用户** · 2026-10-27 22:10",
		"> 📎 附图 2 张",
		"请汇总 **本周** 销量",
		"**🤖 助手** · 2026-10-27 22:11",
		"| 济南 | 12 |",
		"> 🔒 请求批准：write_workspace_file",
		"> ❓ 智能体提问：要覆盖原文件吗？ (是/否)",
		"> 🧩 此处有一张界面卡片",
		"**⚙️ 系统**",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, leaked := range []string{"secret thoughts", "del /f secret.xlsx", "write_workspace_file\"", "更早的消息"} {
		if strings.Contains(got, leaked) {
			t.Errorf("must not contain %q:\n%s", leaked, got)
		}
	}
	if n := strings.Count(got, "\n---\n"); n != 6 {
		t.Errorf("want 6 separated entries (empty and unknown ones skipped), got %d:\n%s", n, got)
	}
	if !strings.Contains(Markdown(sess, nil, time.Now(), true), "（更早的消息未包含）") {
		t.Error("a cut conversation must say so")
	}
	if !strings.HasPrefix(Markdown(Session{}, nil, time.Now(), false), "# 对话\n") {
		t.Error("an untitled conversation needs a heading")
	}
}

func TestSafeFileNameFitsWindows(t *testing.T) {
	cases := []struct{ in, want string }{
		{"销售周报 分析", "销售周报 分析"},
		{`a/b\c:d*e?f"g<h>i|j`, "a_b_c_d_e_f_g_h_i_j"},
		{"  spaced \t out  ", "spaced out"},
		{"trailing dots...", "trailing dots"},
		{"", "对话"},
		{" . . ", "对话"},
		{"CON", "_CON"},
		{"nul.txt", "_nul.txt"},
		{"COM1", "_COM1"},
		{"console", "console"},
	}
	for _, c := range cases {
		if got := SafeFileName(c.in, "对话"); got != c.want {
			t.Errorf("SafeFileName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := SafeFileName(strings.Repeat("名", 100), "x"); len([]rune(got)) != 60 {
		t.Errorf("a long name is cut to 60 runes, got %d", len([]rune(got)))
	}
	if got := CleanTitle(" a \n b "); got != "a b" {
		t.Errorf("CleanTitle = %q", got)
	}
}
