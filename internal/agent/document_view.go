package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"unicode/utf8"
)

// Uses the same spill owner and retrieval tool as generic offloading. This
// projection runs first so expanded metadata cannot trigger a model summary.
type documentViewMiddleware struct {
	adk.TypedBaseChatModelAgentMiddleware[*schema.Message]
	spill *SpillMiddleware
}

func (m *documentViewMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if state == nil {
		return ctx, state, nil
	}
	names := map[string]string{}
	for _, msg := range state.Messages {
		if msg == nil {
			continue
		}
		if msg.Role == schema.Assistant {
			for _, call := range msg.ToolCalls {
				names[call.ID] = call.Function.Name
			}
		}
		if msg.Role != schema.Tool {
			continue
		}
		name := msg.Name
		if name == "" {
			name = names[msg.ToolCallID]
		}
		if name != "parse_file" {
			continue
		}
		view := prepareDocumentModelView(msg.Content)
		if view == nil {
			continue
		}
		raw := msg.Content
		if len(raw)-len(view.render("spill_000000000000")) < minSpillSaving {
			continue
		}
		if record, err := m.spill.store.Save(ctx, SessionIDFromContext(ctx), msg.ToolCallID, name, []byte(raw)); err == nil {
			msg.Content = view.render(record.ID)
			runMeterFrom(ctx).addSpill(len(raw), len(msg.Content))
		}
	}
	return ctx, state, nil
}

// documentModelView keeps every text character, top-level source locator and
// chunk record. Only redundant/per-chunk locators move to the existing spill
// store. It never budgets an arbitrary JSON string by cutting bytes.
type documentModelView struct {
	fields  map[string]json.RawMessage
	omitted int
}

func prepareDocumentModelView(raw string) *documentModelView {
	// A full result must remain readable by the spill store's 1 MiB line reader.
	if len(raw) < defaultSpillMaxBytes || len(raw) > 512*1024 {
		return nil
	}
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &root) != nil || root == nil || root["model_view"] != nil {
		return nil
	}
	var text string
	var source struct {
		Revision string `json:"revision"`
	}
	if json.Unmarshal(root["text"], &text) != nil || text == "" || json.Unmarshal(root["source"], &source) != nil || source.Revision == "" {
		return nil
	}
	view := &documentModelView{fields: root}
	var offset int
	if json.Unmarshal(root["offset"], &offset) != nil || offset < 0 {
		return nil
	}
	windowEnd := offset + utf8.RuneCountInString(text)
	// Keep the full authoritative window locators; remove only an exact duplicate.
	var citation map[string]json.RawMessage
	if json.Unmarshal(root["citation"], &citation) == nil && citation != nil && bytes.Equal(root["source_spans"], citation["source_spans"]) {
		view.omitted += spanCount(citation["source_spans"])
		delete(citation, "source_spans")
		root["citation"], _ = json.Marshal(citation)
	}
	var preview map[string]json.RawMessage
	if json.Unmarshal(root["chunk_preview"], &preview) == nil && preview != nil {
		for _, kind := range []string{"parents", "children"} {
			var records []map[string]json.RawMessage
			if json.Unmarshal(preview[kind], &records) != nil {
				continue
			}
			for _, record := range records {
				var start, end int
				if json.Unmarshal(record["start"], &start) != nil || json.Unmarshal(record["end"], &end) != nil || start < offset || end > windowEnd || end <= start {
					continue
				}
				if n := spanCount(record["source_spans"]); n > 0 {
					view.omitted += n
					delete(record, "source_spans")
					record["source_spans_ref"] = json.RawMessage(`"intersect_document_window_source_spans"`)
				}
			}
			preview[kind], _ = json.Marshal(records)
		}
		root["chunk_preview"], _ = json.Marshal(preview)
	}
	if view.omitted == 0 {
		return nil
	}
	return view
}

func spanCount(raw json.RawMessage) int {
	var spans []json.RawMessage
	if json.Unmarshal(raw, &spans) != nil {
		return 0
	}
	return len(spans)
}

func (view *documentModelView) render(spillID string) string {
	view.fields["model_view"], _ = json.Marshal(struct {
		Kind       string            `json:"kind"`
		Omitted    int               `json:"omitted_source_span_records"`
		Note       string            `json:"note"`
		FullResult map[string]string `json:"full_result"`
	}{"parse_file_structure_v1", view.omitted, "All text and chunk records are retained. Citation locators equal top-level source_spans. For chunks with source_spans_ref, reconstruct locators by intersecting top-level source_spans with the chunk's [start,end) range and clipping span start/end to that intersection. Chunks outside this text window keep their own locators. Fetch the full result for the original expanded metadata.", map[string]string{"tool": "fetch_spill_content", "spill_id": spillID}})
	raw, _ := json.Marshal(view.fields)
	return string(raw)
}
