package tools

import "fmt"

// DocumentChunk is the data-only contract between a chunking plugin and the
// document reader. Offsets refer to the unmodified parsed text, not to a heading
// breadcrumb prepended for embedding. A nil ParentIndex means no parent.
type DocumentChunk struct {
	Start, End    int
	ContextHeader string
	ParentIndex   *int
}
type DocumentChunks struct{ Parents, Children []DocumentChunk }
type DocumentChunker func(text string, childSize int) DocumentChunks

// SetDocumentChunker binds one plugin to this registry. Disposal cannot clear
// a newer binding, and does not resurrect previously disposed bindings.
func (r *Registry) SetDocumentChunker(fn DocumentChunker) func() {
	r.mu.Lock()
	r.documentChunkerGeneration++
	gen := r.documentChunkerGeneration
	r.documentChunker = fn
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.documentChunkerGeneration == gen {
			r.documentChunker = nil
		}
	}
}
func (r *Registry) getDocumentChunker() DocumentChunker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.documentChunker
}

type parsedChunk struct {
	ID            string             `json:"id"`
	Start         int                `json:"start"`
	End           int                `json:"end"`
	ContextHeader string             `json:"context_header,omitempty"`
	ParentID      string             `json:"parent_id,omitempty"`
	Spans         []parsedSourceSpan `json:"source_spans,omitempty"`
}
type parsedChunkPreview struct {
	Status        string        `json:"status"`
	Parents       []parsedChunk `json:"parents,omitempty"`
	Children      []parsedChunk `json:"children,omitempty"`
	TotalChildren int           `json:"total_children,omitempty"`
	// A preview caps metadata separately from text. Resume a parse window here
	// to inspect the next omitted child; next_offset still resumes the text.
	NextPreviewOffset *int `json:"next_preview_offset,omitempty"`
}

func makeParsedChunkPreview(out parseFileOutput, chunks DocumentChunks, start, end int) (parsedChunkPreview, error) {
	total := charCount(out.Text)
	valid := func(ch DocumentChunk) bool { return ch.Start >= 0 && ch.End > ch.Start && ch.End <= total }
	for _, parent := range chunks.Parents {
		if !valid(parent) {
			return parsedChunkPreview{}, fmt.Errorf("document chunker returned invalid parent source range")
		}
	}
	for _, child := range chunks.Children {
		if !valid(child) {
			return parsedChunkPreview{}, fmt.Errorf("document chunker returned invalid child source range")
		}
		if child.ParentIndex != nil {
			i := *child.ParentIndex
			if i < 0 || i >= len(chunks.Parents) || child.Start < chunks.Parents[i].Start || child.End > chunks.Parents[i].End {
				return parsedChunkPreview{}, fmt.Errorf("document chunker returned invalid parent relationship")
			}
		}
	}
	makeChunk := func(kind string, ch DocumentChunk) parsedChunk {
		id := "dc_" + fileSHA256([]byte(fmt.Sprintf("%s:%s:%d:%d:%s", out.Source.Revision, kind, ch.Start, ch.End, ch.ContextHeader)))[:32]
		result := parsedChunk{ID: id, Start: ch.Start, End: ch.End, ContextHeader: ch.ContextHeader}
		for _, span := range out.Spans {
			if span.Start < ch.End && span.End > ch.Start {
				// Limit source evidence to the actual overlap, while keeping full
				// document offsets and the locator's original page/row identity.
				span.Start, span.End = max(span.Start, ch.Start), min(span.End, ch.End)
				result.Spans = append(result.Spans, span)
			}
		}
		return result
	}
	preview := parsedChunkPreview{Status: "ready", TotalChildren: len(chunks.Children)}
	parents := map[int]string{}
	for _, child := range chunks.Children {
		if child.End <= start || child.Start >= end {
			continue
		}
		if len(preview.Children) >= 32 {
			next := child.Start
			preview.NextPreviewOffset = &next
			break
		}
		result := makeChunk("child", child)
		if child.ParentIndex != nil {
			i := *child.ParentIndex
			if parents[i] == "" {
				parent := makeChunk("parent", chunks.Parents[i])
				parents[i] = parent.ID
				preview.Parents = append(preview.Parents, parent)
			}
			result.ParentID = parents[i]
		}
		preview.Children = append(preview.Children, result)
	}
	return preview, nil
}
