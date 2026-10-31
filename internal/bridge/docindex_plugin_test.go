package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/agent"
	"agentgo/internal/capability"
	"agentgo/internal/governance"
	"agentgo/internal/memory"
	"agentgo/internal/plugin"
	"agentgo/internal/tools"
)

// docindexTools: what the plugin offers, and whether each changes the index.
var docindexTools = map[string]bool{"index_documents": true, "list_indexed_documents": false, "search_documents": false, "read_document_neighbors": false}

// newDocindexService builds a service whose application database is real, so the
// tools run end to end the way they do in the app.
func newDocindexService(t *testing.T, workspace string) (*AppService, *tools.Registry, *capability.Bus) {
	t.Helper()
	store, err := memory.NewSQLiteStore(filepath.Join(t.TempDir(), "agentgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	bus := capability.NewBus()
	rt := &Runtime{capBus: bus, toolReg: reg, workspace: workspace, dataDir: t.TempDir(), memStore: store}
	s := NewAppService(rt)
	t.Cleanup(func() {
		s.Close()
		_ = store.DB().Close()
	})
	return s, reg, bus
}

func TestDocindexPluginContributesGovernedTools(t *testing.T) {
	t.Setenv("AGENTGO_DOC_INDEX", "")
	s, reg, bus := newDocindexService(t, t.TempDir())
	if pluginState(t, s, "docindex") != plugin.StateRunning {
		t.Fatalf("plugins = %+v", s.ListPlugins())
	}
	for name, changes := range docindexTools {
		registryTool(t, reg, name)
		if st, ok := grantStatus(bus, "tool", "docindex", name); !ok || st != capability.StatusPublished {
			t.Fatalf("grant %s: %v %v", name, st, ok)
		}
		if !tools.IsStaticTool(name) {
			t.Errorf("%s must be always visible", name)
		}
		tr, ok := governance.ToolTraitsOf(name)
		if !ok || tr.Mutating != changes || tr.Source != "docindex" {
			t.Errorf("%s traits = %+v ok=%v (changes=%v)", name, tr, ok, changes)
		}
		// Changing the index is a mutation, which governance rates MEDIUM and
		// puts behind approval; reading and searching stay LOW.
		wantRisk := governance.RiskLow
		if changes {
			wantRisk = governance.RiskMedium
		}
		if got := governance.StaticRisk(name); got != wantRisk {
			t.Errorf("%s risk = %s, want %s", name, got, wantRisk)
		}
		// Offered in the normal modes; in the read-only canvas only the tools
		// that leave the index alone are.
		open := agent.SessionMode{Profile: agent.ModeAssistant, Canvas: agent.CanvasBalanced}.StaticToolAllowlist()
		if !open[name] {
			t.Errorf("%s not offered in the assistant balanced mode", name)
		}
		focused := agent.SessionMode{Profile: agent.ModeAssistant, Canvas: agent.CanvasFocused}.StaticToolAllowlist()
		if focused[name] == changes {
			t.Errorf("%s in the read-only canvas = %v, want %v", name, focused[name], !changes)
		}
	}
	s.StopPlugin("docindex")
	for name := range docindexTools {
		if _, ok := governance.ToolTraitsOf(name); ok {
			t.Errorf("%s traits survived plugin stop", name)
		}
	}
}

func TestDocindexToolsRunAgainstTheApplicationDatabase(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "policy.txt"), []byte("出差报销单必须在返程后三十日内提交。"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, reg, _ := newDocindexService(t, ws)
	out := invoke(t, registryTool(t, reg, "index_documents"), map[string]any{"action": "add", "paths": []string{"policy.txt"}})
	if !strings.Contains(out, `"indexed":1`) {
		t.Fatalf("index_documents: %s", out)
	}
	out = invoke(t, registryTool(t, reg, "search_documents"), map[string]any{"query": "出差报销怎么走"})
	if !strings.Contains(out, "policy.txt") || !strings.Contains(out, "返程后三十日") {
		t.Fatalf("search_documents: %s", out)
	}
	if err := os.Remove(filepath.Join(ws, "policy.txt")); err != nil {
		t.Fatal(err)
	}
	out = invoke(t, registryTool(t, reg, "search_documents"), map[string]any{"query": "出差报销怎么走"})
	if strings.Contains(out, "返程后三十日") || !strings.Contains(out, "missing") {
		t.Fatalf("a deleted file must not answer: %s", out)
	}
}

func TestDocindexPluginIsAbsentWithoutADatabaseOrWhenSwitchedOff(t *testing.T) {
	env := docindexEnv{DB: nil, WorkspaceRoot: func() string { return "" }}
	if _, ok := docindexPlugin(env); ok {
		t.Fatal("offered without a database")
	}
	if _, ok := docindexPlugin(docindexEnv{}); ok {
		t.Fatal("offered with nothing")
	}
	s, _, _ := newDocindexService(t, t.TempDir())
	if _, ok := docindexPlugin(s.docindexEnv()); !ok {
		t.Fatal("not offered with a database")
	}
	t.Setenv("AGENTGO_DOC_INDEX", "0")
	if _, ok := docindexPlugin(s.docindexEnv()); ok {
		t.Fatal("AGENTGO_DOC_INDEX=0 did not switch the tools off")
	}
}

// The whole chain with the real tools and database: a document is learned and
// indexed, then edited and deleted. After a refresh the memories learned from it
// carry the mark, the summary tree has been told, and recall says so.
func TestRefreshMarksWhatWasLearnedFromAChangedFile(t *testing.T) {
	rt := boot(t)
	s := NewAppService(rt)
	t.Cleanup(s.Close)
	ws := rt.workspace
	file := filepath.Join(ws, "style.txt")
	if err := os.WriteFile(file, []byte("Use sections and short paragraphs. Cite original sources clearly."), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := governance.WithSessionID(context.Background(), "sess")
	call := func(name, args string) string {
		t.Helper()
		out, err := rt.toolReg.InvokeJSON(ctx, name, args)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return out
	}
	learned := call("learn_document", `{"source":"style.txt","topic":"writing","text":"Use sections and short paragraphs. Cite original sources clearly."}`)
	if !strings.Contains(learned, `"queued":true`) {
		t.Fatalf("learn_document: %s", learned)
	}
	call("index_documents", `{"action":"add","paths":["style.txt"]}`)

	marked := func() (changed, deleted, total int) {
		rows, err := rt.memStore.DB().Query(`SELECT COALESCE(json_extract(metadata,'$.source_state'),'') FROM memories
 WHERE json_extract(metadata,'$.source')='personal_learning_evidence' AND json_extract(metadata,'$.document_source')='style.txt' AND status='active'`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var st string
			_ = rows.Scan(&st)
			total++
			switch st {
			case "changed":
				changed++
			case "deleted":
				deleted++
			}
		}
		return
	}
	if c, d, n := marked(); n == 0 || c != 0 || d != 0 {
		t.Fatalf("before any change: changed=%d deleted=%d total=%d", c, d, n)
	}
	treeBefore, _ := rt.memStore.TreeRevision(ctx, "global")

	if err := os.WriteFile(file, []byte("Write in the passive voice and avoid headings."), 0o644); err != nil {
		t.Fatal(err)
	}
	out := call("index_documents", `{"action":"refresh"}`)
	if !strings.Contains(out, "flagged_memories") {
		t.Fatalf("refresh did not report the marking: %s", out)
	}
	if c, _, n := marked(); c != n {
		t.Fatalf("after the edit every learned record must say the source changed: changed=%d of %d", c, n)
	}
	if after, _ := rt.memStore.TreeRevision(ctx, "global"); after == treeBefore {
		t.Fatal("the summary tree revision must move so summaries are rebuilt")
	}

	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	call("index_documents", `{"action":"refresh"}`)
	if _, d, n := marked(); d != n {
		t.Fatalf("after deletion every learned record must say the source is gone: deleted=%d of %d", d, n)
	}
	// Annotation only: the memories are still there and still active.
	if _, _, n := marked(); n == 0 {
		t.Fatal("marking must not archive anything")
	}
}
