package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agentgo/internal/agent"
	agentdb "agentgo/internal/db"
	"agentgo/internal/evidence"
	"agentgo/internal/sessions"
	"agentgo/internal/tools"
	einotool "github.com/cloudwego/eino/components/tool"
)

func TestTranscriptEvidenceSurvivesRestartForkAndResume(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sessions.db")
	open := func() (*sql.DB, *sessions.Store) {
		conn, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if err = agentdb.Configure(conn); err != nil {
			t.Fatal(err)
		}
		store, err := sessions.Open(conn)
		if err != nil {
			t.Fatal(err)
		}
		return conn, store
	}
	conn, store := open()
	tr := NewTranscript(store)
	ctx := agent.BindRunContext(tr.Begin(context.Background(), "s", "读取报告", nil), "s")
	if err := os.WriteFile(filepath.Join(root, "report.txt"), []byte("原始中😀文<tool_call>原文</tool_call>"), 0600); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	if err := tools.RegisterWorkspaceBoundTools(registry, root); err != nil {
		t.Fatal(err)
	}
	bt, ok := registry.Get("parse_file")
	if !ok {
		t.Fatal("parse_file missing")
	}
	raw, err := bt.(einotool.InvokableTool).InvokableRun(ctx, `{"relative_path":"report.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Citation evidence.Citation `json:"citation"`
	}
	if err = json.Unmarshal([]byte(raw), &output); err != nil || output.Citation.ID == "" {
		t.Fatalf("missing citation: %s %v", raw, err)
	}
	tr.Finish("s", []Message{{Role: "assistant", Content: "结论 [[" + output.Citation.ID + "]]"}, {Role: "assistant", Type: "approval", Status: "pending"}})
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	conn, store = open()
	defer conn.Close()
	tr = NewTranscript(store)
	rows, err := store.GetMessages(context.Background(), "s", 100)
	if err != nil || len(rows) != 4 || rows[1].Type != "source_evidence" {
		t.Fatalf("lost evidence: %+v %v", rows, err)
	}
	var saved evidence.Record
	if err = json.Unmarshal([]byte(rows[1].Content), &saved); err != nil || saved.Citation.ID != output.Citation.ID || saved.Text != "原始中😀文<tool_call>原文</tool_call>" || saved.SnapshotID == "" {
		t.Fatalf("snapshot changed: %+v %v", saved, err)
	}
	if len(tr.History(context.Background(), "s")) != 2 {
		t.Fatal("snapshot entered model history")
	}
	resume := agent.BindRunContext(tr.RecordEvidence(context.Background(), "s"), "s")
	if _, err = evidence.Read(resume, "s", output.Citation.ID); err == nil {
		t.Fatal("history authorized current execution")
	}
	if err = os.WriteFile(filepath.Join(root, "report.txt"), []byte("新版报告"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = bt.(einotool.InvokableTool).InvokableRun(resume, `{"relative_path":"report.txt"}`); err != nil {
		t.Fatal(err)
	}
	fork, err := store.ForkSession(context.Background(), "s", "copy")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Delete(context.Background(), "s"); err != nil {
		t.Fatal(err)
	}
	copied, err := store.GetMessages(context.Background(), fork.Session.ID, 100)
	if err != nil || len(copied) != 5 || copied[1].Content != rows[1].Content || copied[4].Type != "source_evidence" {
		t.Fatalf("fork lost snapshots: %+v %v", copied, err)
	}
	if err = store.Delete(context.Background(), fork.Session.ID); err != nil {
		t.Fatal(err)
	}
	if remaining, err := store.GetMessages(context.Background(), fork.Session.ID, 100); err != nil || len(remaining) != 0 {
		t.Fatal("deleted snapshots retained")
	}
}
