package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/governance"
	"github.com/cloudwego/eino/components/tool"
)

func TestEvidenceIsolationAndBounds(t *testing.T) {
	ctx := WithEvidenceRun(governance.WithSessionID(context.Background(), "one"))
	c := EvidenceCitation{Kind: "file", Source: "a.txt", Revision: strings.Repeat("a", 64), Start: 2, End: 5}
	ref := registerEvidence(ctx, c, "中😀文")
	if ref == nil || registerEvidence(ctx, c, "中😀文").ID != ref.ID {
		t.Fatal("missing or unstable reference")
	}
	if record, err := readEvidence(WithEvidenceRun(ctx), ref.ID); err != nil || record.Text != "中😀文" || !record.Untrusted {
		t.Fatalf("lost snapshot: %+v %v", record, err)
	}
	for _, other := range []context.Context{WithEvidenceRun(context.Background()), governance.WithSessionID(ctx, "two"), WithEvidenceRun(governance.WithSessionID(ctx, "two"))} {
		if _, err := readEvidence(other, ref.ID); err == nil {
			t.Fatal("cross-run/session evidence leaked")
		}
	}
	if registerEvidence(ctx, c, "too long") != nil || registerEvidence(context.Background(), c, "中😀文") != nil {
		t.Fatal("invalid evidence accepted")
	}
	for i := 0; i < 127; i++ {
		c.Source = fmt.Sprint(i)
		if registerEvidence(ctx, c, "中😀文") == nil {
			t.Fatal("early registry limit")
		}
	}
	c.Source = "overflow"
	if registerEvidence(ctx, c, "中😀文") != nil {
		t.Fatal("unbounded registry")
	}
	if _, err := readEvidence(ctx, ref.ID); err != nil {
		t.Fatal("bounded registry evicted an existing citation")
	}
}

func TestEvidenceActualParseToolAndSavedRevision(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "source.txt")
	if err := os.WriteFile(file, []byte("前中😀文后"), 0600); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	if err := registerParseFile(r, root); err != nil {
		t.Fatal(err)
	}
	if err := registerReadEvidence(r); err != nil {
		t.Fatal(err)
	}
	ctx := WithEvidenceRun(context.Background())
	var parse, read tool.InvokableTool
	for _, candidate := range r.GetAllTools() {
		info, _ := candidate.Info(ctx)
		if info.Name == "parse_file" {
			parse = candidate.(tool.InvokableTool)
		}
		if info.Name == "read_evidence" {
			read = candidate.(tool.InvokableTool)
		}
	}
	if parse == nil || read == nil {
		t.Fatal("missing registered tool")
	}
	raw, err := parse.InvokableRun(ctx, `{"relative_path":"source.txt","offset":1,"limit":3}`)
	if err != nil {
		t.Fatal(err)
	}
	var out parseFileOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Citation == nil || out.Citation.Start != 1 || out.Citation.End != 4 {
		t.Fatalf("bad citation: %s", raw)
	}
	if err := os.WriteFile(file, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err = read.InvokableRun(ctx, fmt.Sprintf(`{"id":%q}`, out.Citation.ID))
	if err != nil {
		t.Fatal(err)
	}
	var saved evidenceRecord
	if err := json.Unmarshal([]byte(raw), &saved); err != nil || saved.Text != "中😀文" || saved.Citation.Revision != out.Source.Revision {
		t.Fatalf("snapshot drift: %s %v", raw, err)
	}
	if _, err := read.InvokableRun(WithEvidenceRun(context.Background()), fmt.Sprintf(`{"id":%q}`, out.Citation.ID)); err == nil {
		t.Fatal("old-run ID accepted")
	}
}

func TestEvidenceWebRegistersOnlyReturnedBatchWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, strings.Repeat("中😀", 4000))
	}))
	defer server.Close()
	reader := newWebPageReader(server.Client())
	ctx := WithEvidenceRun(context.Background())
	out, err := reader.readBatch(ctx, webFetchInput{Items: []webFetchItem{{URL: server.URL}, {URL: server.URL}, {URL: server.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, result := range out.Results {
		if result.Citation == nil {
			t.Fatalf("no citation: %+v", result)
		}
		saved, err := readEvidence(ctx, result.Citation.ID)
		if err != nil || saved.Text != result.Text || saved.Citation.End != charCount(result.Text) {
			t.Fatal("registered unread content")
		}
		total += charCount(result.Text)
	}
	if total > webBatchBudget || total < webBatchBudget-3 {
		t.Fatalf("bad batch budget %d", total)
	}
}
