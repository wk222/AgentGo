package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/docchunk"
	"agentgo/internal/memory"
	"agentgo/internal/plugin"
)

func TestChunkerPluginPreservesStructuredPreviewAndUnhooksOnStop(t *testing.T) {
	t.Setenv("AGENTGO_DOCCHUNK", "1")
	rt := boot(t)
	text := "# 合同\n\n" + strings.Repeat("合同金额与付款期限。", 80) + "\n\n## 违约责任\n\n" + strings.Repeat("发生违约后应承担的责任。", 80)
	if err := os.WriteFile(filepath.Join(rt.workspace, "preview.md"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	read := func() struct {
		ChunkPreview struct {
			Status  string `json:"status"`
			Parents []struct {
				ID string `json:"id"`
			} `json:"parents"`
			Children []struct {
				Start    int    `json:"start"`
				End      int    `json:"end"`
				Header   string `json:"context_header"`
				ParentID string `json:"parent_id"`
			} `json:"children"`
		} `json:"chunk_preview"`
	} {
		raw, err := rt.toolReg.InvokeJSON(context.Background(), "parse_file", `{"relative_path":"preview.md","preview_chunks":true,"chunk_size":128}`)
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			ChunkPreview struct {
				Status  string `json:"status"`
				Parents []struct {
					ID string `json:"id"`
				} `json:"parents"`
				Children []struct {
					Start    int    `json:"start"`
					End      int    `json:"end"`
					Header   string `json:"context_header"`
					ParentID string `json:"parent_id"`
				} `json:"children"`
			} `json:"chunk_preview"`
		}
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := read()
	if out.ChunkPreview.Status != "ready" || len(out.ChunkPreview.Children) < 2 || len(out.ChunkPreview.Parents) == 0 {
		t.Fatalf("plugin not connected to structured parsing: %+v", out)
	}
	withHeader := false
	for _, ch := range out.ChunkPreview.Children {
		if ch.Start < 0 || ch.End > len([]rune(text)) || ch.End <= ch.Start {
			t.Fatalf("bad source offsets: %+v", ch)
		}
		if ch.Header != "" {
			withHeader = true
		}
		if ch.ParentID == "" {
			t.Fatalf("expected parent relationship: %+v", ch)
		}
	}
	if !withHeader {
		t.Fatal("heading breadcrumb was lost")
	}
	if err := rt.host.Stop("chunker"); err != nil {
		t.Fatal(err)
	}
	if after := read(); after.ChunkPreview.Status != "unavailable" {
		t.Fatal("structured preview survived plugin stop")
	}
}

// The chunker plugin exists so memory can split long text on document structure
// instead of on blank lines. These tests pin the things that would break
// silently: the service is reachable on the bus, the hook memory reads is
// actually installed, and stopping the plugin takes the hook back out.

// chunkerFixture is long enough that both splitters have to split it, and it has
// blank-line paragraphs so the built-in splitter and the adaptive one disagree on
// where the boundaries go.
func chunkerFixture() string {
	return strings.Repeat("段落一。这是一段用于测试的中文内容，需要足够长才能触发切分逻辑。\n\n", 40)
}

func TestChunkerPluginProvidesServiceAndHooksMemory(t *testing.T) {
	t.Setenv("AGENTGO_DOCCHUNK", "1")
	rt := boot(t)

	st, ok := statusMap(rt)["chunker"]
	if !ok || st.State != plugin.StateRunning {
		t.Fatalf("chunker plugin: want running, got %+v (present=%v)", st, ok)
	}
	if st.Protected {
		t.Error("chunker must not be Protected: the runtime works without it")
	}
	svc, err := plugin.Resolve[*docchunk.Service](rt.host, svcChunker)
	if err != nil {
		t.Fatalf("chunker service not on the bus: %v", err)
	}

	// Asserting the hook, not just the service: memory reads it at ingest time,
	// and nothing else connects the two.
	text := chunkerFixture()
	viaHook := memory.SplitContent(text, 200, 0)
	direct := svc.Chunk(text, 200, 0)
	if len(viaHook) != len(direct) {
		t.Fatalf("memory is not using the chunker hook: hook split into %d chunks, service into %d",
			len(viaHook), len(direct))
	}
	if len(direct) < 2 {
		t.Fatalf("expected the fixture to split, got %d chunk(s)", len(direct))
	}
}

func TestChunkerPluginRestoresMemorySplitterOnStop(t *testing.T) {
	t.Setenv("AGENTGO_DOCCHUNK", "1")
	rt := boot(t)

	text := chunkerFixture()
	withHook := memory.SplitContent(text, 200, 0)
	builtIn := memory.ChunkContent(text, 200, 0)
	if equalStrings(withHook, builtIn) {
		t.Fatal("fixture is useless: the adaptive and built-in splitters agree on it")
	}

	if err := rt.host.Stop("chunker"); err != nil {
		t.Fatalf("stop chunker: %v", err)
	}
	if _, err := plugin.Resolve[*docchunk.Service](rt.host, svcChunker); err == nil {
		t.Error("chunker service is still on the bus after Stop")
	}
	if after := memory.SplitContent(text, 200, 0); !equalStrings(after, builtIn) {
		t.Errorf("memory kept the adaptive splitter after Stop: %d chunks, want the built-in %d",
			len(after), len(builtIn))
	}
}

func TestChunkerPluginIsOffByDefault(t *testing.T) {
	// The default is off on purpose: switching splitters changes recall, so it is
	// opted into only after an A/B against docs/eval-baseline.md.
	t.Setenv("AGENTGO_DOCCHUNK", "0")
	rt := boot(t)
	if st, ok := statusMap(rt)["chunker"]; ok && st.State == plugin.StateRunning {
		t.Error("chunker plugin started although AGENTGO_DOCCHUNK=0")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
