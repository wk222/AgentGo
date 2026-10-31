package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentgo/internal/promptbudget"
	"agentgo/internal/spill"
	"agentgo/internal/tools"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/xuri/excelize/v2"
)

func documentViewFixture(t *testing.T, window ...int) (string, time.Duration) {
	t.Helper()
	root := t.TempDir()
	f := excelize.NewFile()
	for row := 1; row <= 300; row++ {
		if err := f.SetSheetRow("Sheet1", fmt.Sprintf("A%d", row), &[]any{fmt.Sprintf("区县%03d", row), 1000 + row, "核对中😀文原始记录"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.SaveAs(filepath.Join(root, "report.xlsx")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	r := tools.NewRegistry()
	if err := tools.RegisterWorkspaceBoundTools(r, root); err != nil {
		t.Fatal(err)
	}
	r.SetDocumentChunker(func(text string, _ int) tools.DocumentChunks {
		var chunks tools.DocumentChunks
		length := len([]rune(text))
		for start := 0; start < length; start += 2048 {
			end := min(start+2048, length)
			pi := len(chunks.Parents)
			chunks.Parents = append(chunks.Parents, tools.DocumentChunk{Start: start, End: end, ContextHeader: "Sheet1"})
			for child := start; child < end; child += 432 {
				index := pi
				chunks.Children = append(chunks.Children, tools.DocumentChunk{Start: child, End: min(child+512, end), ParentIndex: &index, ContextHeader: "Sheet1"})
			}
		}
		return chunks
	})
	bt, ok := r.Get("parse_file")
	if !ok {
		t.Fatal("missing parse_file")
	}
	started := time.Now()
	args := map[string]any{"relative_path": "report.xlsx", "preview_chunks": true, "limit": 32000}
	if len(window) == 2 {
		args["offset"] = window[0]
		args["limit"] = window[1]
	}
	input, _ := json.Marshal(args)
	raw, err := bt.(tool.InvokableTool).InvokableRun(BindRunContext(context.Background(), "view-test"), string(input))
	if err != nil {
		t.Fatal(err)
	}
	return raw, time.Since(started)
}

func TestDocumentViewBaseline(t *testing.T) {
	raw, duration := documentViewFixture(t)
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatal(err)
	}
	preview := data["chunk_preview"].(map[string]any)
	t.Logf("baseline bytes=%d estimated_tokens=%d text_runes=%d children=%d parse_wall=%s", len(raw), promptbudget.Estimate(raw), len([]rune(data["text"].(string))), len(preview["children"].([]any)), duration)
}

func TestDocumentViewReplayKeepsBodyRecordsAndScopedFullResult(t *testing.T) {
	raw, parseWall := documentViewFixture(t)
	var original map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &original); err != nil {
		t.Fatal(err)
	}
	store, err := BuildDefaultSpillStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mw, err := NewSpillMiddleware(store)
	if err != nil {
		t.Fatal(err)
	}
	meter := &RunMeter{}
	ctx := WithRunMeter(BindRunContext(context.Background(), "view-test"), meter)
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{{Role: schema.Tool, Name: "parse_file", ToolCallID: "c", Content: raw}}}
	started := time.Now()
	projector := &documentViewMiddleware{spill: mw}
	_, state, err = projector.BeforeModelRewriteState(ctx, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	viewWall := time.Since(started)
	projected := state.Messages[0].Content
	var view map[string]json.RawMessage
	if err = json.Unmarshal([]byte(projected), &view); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"text", "source", "source_spans", "offset", "total_chars", "next_offset", "char_count", "file_type"} {
		if string(original[key]) != string(view[key]) {
			t.Fatalf("changed %s", key)
		}
	}
	for _, kind := range []string{"parents", "children"} {
		var before, after struct{ Parents, Children []map[string]json.RawMessage }
		if err = json.Unmarshal(original["chunk_preview"], &before); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(view["chunk_preview"], &after); err != nil {
			t.Fatal(err)
		}
		left, right := before.Parents, after.Parents
		if kind == "children" {
			left, right = before.Children, after.Children
		}
		if len(left) != len(right) {
			t.Fatal("chunk records disappeared")
		}
		for i := range left {
			for _, key := range []string{"id", "start", "end", "parent_id", "context_header"} {
				if string(left[i][key]) != string(right[i][key]) {
					t.Fatalf("lost %s record %d %s", kind, i, key)
				}
			}
			if right[i]["source_spans_ref"] != nil {
				var spans []map[string]json.RawMessage
				if err = json.Unmarshal(view["source_spans"], &spans); err != nil {
					t.Fatal(err)
				}
				var start, end int
				json.Unmarshal(right[i]["start"], &start)
				json.Unmarshal(right[i]["end"], &end)
				var reconstructed []map[string]json.RawMessage
				for _, span := range spans {
					var a, b int
					json.Unmarshal(span["start"], &a)
					json.Unmarshal(span["end"], &b)
					if a < end && b > start {
						span["start"], _ = json.Marshal(max(a, start))
						span["end"], _ = json.Marshal(min(b, end))
						reconstructed = append(reconstructed, span)
					}
				}
				var expected any
				json.Unmarshal(left[i]["source_spans"], &expected)
				raw, _ := json.Marshal(reconstructed)
				var actual any
				json.Unmarshal(raw, &actual)
				if !reflect.DeepEqual(expected, actual) {
					t.Fatalf("cannot reconstruct %s chunk %d", kind, i)
				}
			}
		}
	}
	var metadata struct {
		Omitted int `json:"omitted_source_span_records"`
		Full    struct {
			ID string `json:"spill_id"`
		} `json:"full_result"`
	}
	if err = json.Unmarshal(view["model_view"], &metadata); err != nil || metadata.Omitted == 0 || metadata.Full.ID == "" {
		t.Fatal("missing omission/retrieval metadata", err)
	}
	_, agentCtx, err := mw.BeforeAgent(ctx, &adk.ChatModelAgentContext{})
	if err != nil || len(agentCtx.Tools) != 1 {
		t.Fatal("reader not mounted", err)
	}
	reader := agentCtx.Tools[0].(tool.InvokableTool)
	args := fmt.Sprintf(`{"spill_id":%q,"limit_lines":1}`, metadata.Full.ID)
	readStarted := time.Now()
	full, err := reader.InvokableRun(ctx, args)
	readWall := time.Since(readStarted)
	if err != nil {
		t.Fatal(err)
	}
	var fetched struct {
		Content string `json:"content"`
	}
	if err = json.Unmarshal([]byte(full), &fetched); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(fetched.Content, " | "+raw) {
		t.Fatal("full result changed")
	}
	if _, err = reader.InvokableRun(BindRunContext(context.Background(), "other"), args); err == nil {
		t.Fatal("cross-session full result exposed")
	}
	if _, _, err = projector.BeforeModelRewriteState(ctx, state, nil); err != nil || state.Messages[0].Content != projected || meter.Snapshot().Spills != 1 {
		t.Fatal("repeated view drift", err)
	}
	var body string
	if err = json.Unmarshal(view["text"], &body); err != nil || !strings.Contains(body, "区县150") {
		t.Fatal("middle answer evidence missing")
	}
	t.Logf("replay raw_bytes=%d view_bytes=%d raw_estimated_tokens=%d view_estimated_tokens=%d parse_wall=%s projection_wall=%s body_lookup_extra_calls=0 metadata_full_read_calls=1 total_with_metadata_read_estimate=%d", len(raw), len(projected), promptbudget.Estimate(raw), promptbudget.Estimate(projected), parseWall, viewWall, promptbudget.Estimate(projected)+promptbudget.Estimate(full))
	t.Logf("CPU replay body_path_wall=%s expanded_metadata_path_wall=%s full_read_wall=%s (no model inference)", parseWall+viewWall, parseWall+viewWall+readWall, readWall)
	if len(projected) >= len(raw) || promptbudget.Estimate(projected) >= promptbudget.Estimate(raw) {
		t.Fatal("no model-view saving")
	}
}

type failedDocumentSpill struct{ spill.Store }

func (failedDocumentSpill) Save(context.Context, string, string, string, []byte) (*spill.SpillRecord, error) {
	return nil, errors.New("disk full")
}

func TestDocumentViewStorageFailureKeepsOriginal(t *testing.T) {
	raw, _ := documentViewFixture(t)
	mw, err := NewSpillMiddleware(failedDocumentSpill{})
	if err != nil {
		t.Fatal(err)
	}
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{{Role: schema.Tool, Name: "parse_file", Content: raw}}}
	if _, _, err = (&documentViewMiddleware{spill: mw}).BeforeModelRewriteState(BindRunContext(context.Background(), "s"), state, nil); err != nil || state.Messages[0].Content != raw {
		t.Fatal("unrecoverable projection issued", err)
	}
}

func TestDocumentViewKeepsLocationsOutsideReturnedWindow(t *testing.T) {
	raw, _ := documentViewFixture(t, 4096, 512)
	view := prepareDocumentModelView(raw)
	if view == nil {
		t.Fatal("missing window view")
	}
	var original, projected struct {
		ChunkPreview struct{ Parents, Children []map[string]json.RawMessage } `json:"chunk_preview"`
	}
	if err := json.Unmarshal([]byte(raw), &original); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(view.render("test")), &projected); err != nil {
		t.Fatal(err)
	}
	kept := 0
	for i, parent := range original.ChunkPreview.Parents {
		var start, end int
		json.Unmarshal(parent["start"], &start)
		json.Unmarshal(parent["end"], &end)
		if start < 4096 || end > 4608 {
			if !bytes.Equal(parent["source_spans"], projected.ChunkPreview.Parents[i]["source_spans"]) {
				t.Fatal("outside-window locators lost")
			}
			kept++
		}
	}
	if kept == 0 {
		t.Fatal("fixture did not exercise outside-window parent")
	}
}

type documentViewModel struct {
	sawView bool
	calls   int
}

func (m *documentViewModel) Generate(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.calls++
	for _, message := range messages {
		if message.Role == schema.Tool {
			m.sawView = strings.Contains(message.Content, `"model_view"`) && strings.Contains(message.Content, "区县150")
			return schema.AssistantMessage("done", nil), nil
		}
	}
	return schema.AssistantMessage("", []schema.ToolCall{{ID: "parse", Type: "function", Function: schema.FunctionCall{Name: "parse_file", Arguments: "{}"}}}), nil
}
func (m *documentViewModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := m.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}
func (m *documentViewModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

type documentViewObserver struct{ raw string }

func (*documentViewObserver) ToolStarted(string, string, string) {}
func (o *documentViewObserver) ToolFinished(_ string, name, output string, _ bool) {
	if name == "parse_file" {
		o.raw = output
	}
}

func TestRealADKDocumentViewLeavesObserverFullOutput(t *testing.T) {
	raw, _ := documentViewFixture(t)
	ctx := BindRunContext(context.Background(), "view-test")
	observer := &documentViewObserver{}
	ctx = WithToolObserver(ctx, observer)
	r := newPipelineTestRunner(t)
	m := &documentViewModel{}
	handlers, err := r.BuildADKHandlers(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := utils.InferTool("parse_file", "test source", func(context.Context, struct{}) (json.RawMessage, error) { return json.RawMessage(raw), nil })
	if err != nil {
		t.Fatal(err)
	}
	a, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "document_view", Description: "test", Model: m, Handlers: handlers, ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{reader}}}, MaxIterations: 3})
	if err != nil {
		t.Fatal(err)
	}
	it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: a}).Run(ctx, []adk.Message{schema.UserMessage("查看区县150原始记录")})
	for {
		event, ok := it.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			t.Fatal(event.Err)
		}
	}
	if !m.sawView || observer.raw != raw || m.calls != 2 {
		t.Fatalf("model projection or original UI output lost: view=%v raw_equal=%v model_calls=%d", m.sawView, observer.raw == raw, m.calls)
	}
}
