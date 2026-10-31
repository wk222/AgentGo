package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"agentgo/internal/memory"
	einotool "github.com/cloudwego/eino/components/tool"
)

func invokeLearningTool(t *testing.T, r *Registry, name string, input any) string {
	t.Helper()
	ctx := context.Background()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, bt := range r.GetAllTools() {
		info, err := bt.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.Name == name {
			tool, ok := bt.(einotool.InvokableTool)
			if !ok {
				t.Fatal("not invokable")
			}
			out, err := tool.InvokableRun(ctx, string(raw))
			if err != nil {
				t.Fatal(err)
			}
			return out
		}
	}
	t.Fatal("tool missing", name)
	return ""
}
func TestDocumentLearningPreservesAllSectionsAndRejectsMissingSource(t *testing.T) {
	s, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	p := memory.NewPipeline(s)
	r := NewRegistry()
	if err = RegisterDocumentLearningTools(r, p, func(context.Context) string { return "office-session" }, nil); err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("公文范文正文及格式。 \n", 900)
	out := invokeLearningTool(t, r, "learn_document", learnDocumentInput{Source: "report.docx", Topic: "工作报告", Text: text})
	var result learnDocumentOutput
	if err = json.Unmarshal([]byte(out), &result); err != nil || !result.Queued || len(result.SourceIDs) < 3 {
		t.Fatal(out, err)
	}
	var rebuilt strings.Builder
	for _, id := range result.SourceIDs {
		rec, err := s.GetRecord(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		rebuilt.WriteString(rec.Content)
	}
	if rebuilt.String() != text {
		t.Fatal("document truncated")
	}
	out = invokeLearningTool(t, r, "learn_document", learnDocumentInput{Text: "missing original source reference"})
	if !strings.Contains(out, "source and text required") {
		t.Fatal(out)
	}
	out = invokeLearningTool(t, r, "memory_learning_status", struct{}{})
	if !strings.Contains(out, fmt.Sprintf(`"pending":%d`, len(result.SourceIDs))) {
		t.Fatal(out)
	}
}
func TestRememberDefaultModalityIsFact(t *testing.T) {
	r := NewRegistry()
	var saved memory.Record
	if err := RegisterRememberTool(r, func(_ context.Context, rec memory.Record) error { saved = rec; return nil }); err != nil {
		t.Fatal(err)
	}
	invokeLearningTool(t, r, "remember", rememberInput{Content: "Reports should retain source references"})
	if saved.Modality != "fact" {
		t.Fatal(saved.Modality)
	}
}

func TestDocumentLearningToolRefreshAndDelete(t *testing.T) {
	s, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	r := NewRegistry()
	if err = RegisterDocumentLearningTools(r, memory.NewPipeline(s), func(context.Context) string { return "s" }, nil); err != nil {
		t.Fatal(err)
	}
	call := func(in learnDocumentInput) learnDocumentOutput {
		var out learnDocumentOutput
		if err := json.Unmarshal([]byte(invokeLearningTool(t, r, "learn_document", in)), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	a := call(learnDocumentInput{Source: "doc", Text: "First document with detailed sections."})
	if a.Error != "" || !a.Changed || !a.Queued || len(a.Revision) != 64 {
		t.Fatal(a)
	}
	retry := call(learnDocumentInput{Source: "doc", Text: "First document with detailed sections.", ExpectedRevision: a.Revision})
	if retry.Error != "" || retry.Changed || retry.SourceIDs[0] != a.SourceIDs[0] {
		t.Fatal(retry)
	}
	b := call(learnDocumentInput{Source: "doc", Text: "Second document with revised headings.", ExpectedRevision: a.Revision})
	if b.Error != "" || !b.Changed || b.Revision == a.Revision {
		t.Fatal(b)
	}
	old, err := s.GetRecord(context.Background(), a.SourceIDs[0])
	if err != nil || old.Status != "archived" {
		t.Fatal(old, err)
	}
	stale := call(learnDocumentInput{Source: "doc", Operation: "delete", ExpectedRevision: a.Revision})
	if !strings.Contains(stale.Error, "source_changed") {
		t.Fatal(stale)
	}
	deleted := call(learnDocumentInput{Source: "doc", Operation: "delete", ExpectedRevision: b.Revision})
	if deleted.Error != "" || !deleted.Deleted || !deleted.Changed || deleted.Queued {
		t.Fatal(deleted)
	}
	for _, in := range []learnDocumentInput{{Source: "doc", Operation: "invalid"}, {Source: "doc", Operation: "delete", Text: "do not silently ignore this text"}} {
		if out := call(in); out.Error == "" {
			t.Fatal("invalid operation accepted", in)
		}
	}
}
