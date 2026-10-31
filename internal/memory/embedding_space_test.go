package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// embedServer answers /embeddings from a fixed text->vector table. Input that
// contains failOn gets an HTTP error.
func embedServer(t *testing.T, vecs map[string][]float64, failOn string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if failOn != "" && strings.Contains(req.Input, failOn) {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		v, ok := vecs[req.Input]
		if !ok {
			v = []float64{0, 0, 1}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": v}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func memStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DB().Close() })
	return s
}

func ids(rows []Record) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func putVec(t *testing.T, s *SQLiteStore, id, content string, meta map[string]interface{}) {
	t.Helper()
	if err := s.Ingest(context.Background(), Record{ID: id, Content: content, Scope: "s", Modality: "journal", Status: "active", Metadata: meta}); err != nil {
		t.Fatal(err)
	}
}

func stamped(vec []float32, space, text string) map[string]interface{} {
	m := map[string]interface{}{}
	stampEmbedding(m, vec, space, text)
	return m
}

func TestRecallWithEmbeddingIgnoresVectorsItCannotVouchFor(t *testing.T) {
	srv := embedServer(t, map[string][]float64{"budget plan": {1, 0, 0}}, "")
	emb := NewEmbedder(EmbeddingConfig{APIBase: srv.URL, APIKey: "k", Model: "m1"})
	space := emb.Space()
	s := memStore(t)

	perfect := []float32{1, 0, 0}
	putVec(t, s, "good", "budget plan good", stamped([]float32{0.9, 0.1, 0}, space, "budget plan good"))
	// Same dimension and a perfect match, but computed by a different model.
	putVec(t, s, "other-model", "budget plan other", stamped(perfect, embeddingSpace("m2", srv.URL), "budget plan other"))
	// Written before spaces were recorded.
	putVec(t, s, "legacy", "budget plan legacy", map[string]interface{}{metaEmbedding: perfect})
	// Vector belongs to the text this record had before it was edited.
	putVec(t, s, "stale", "budget plan stale edited", stamped(perfect, space, "budget plan stale original"))
	// Label claims our space but the vector has two coordinates, not three.
	wrongDim := stamped([]float32{1, 0}, space, "budget plan short")
	putVec(t, s, "short", "budget plan short", wrongDim)
	putVec(t, s, "none", "budget plan none", nil)

	got, err := NewPipeline(s).RecallWithEmbedding(context.Background(), "budget plan", RecallOptions{Scope: "s", Limit: 10}, emb)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"good"}; strings.Join(ids(got), ",") != strings.Join(want, ",") {
		t.Fatalf("only the vector that can prove its space and text may rank, got %v", ids(got))
	}
}

func TestRecallWithEmbeddingKeepsFullTextWhenNoVectorIsUsable(t *testing.T) {
	srv := embedServer(t, map[string][]float64{"budget plan": {1, 0, 0}}, "")
	emb := NewEmbedder(EmbeddingConfig{APIBase: srv.URL, APIKey: "k", Model: "m1"})
	s := memStore(t)
	putVec(t, s, "a", "budget plan a", map[string]interface{}{metaEmbedding: []float32{1, 0, 0}})
	putVec(t, s, "b", "budget plan b", stamped([]float32{1, 0, 0}, embeddingSpace("m2", srv.URL), "budget plan b"))

	got, err := NewPipeline(s).RecallWithEmbedding(context.Background(), "budget plan", RecallOptions{Scope: "s", Limit: 10}, emb)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("full-text results must survive when no vector is usable, got %v", ids(got))
	}
	reasons := map[string]string{}
	for _, r := range got {
		_, reasons[r.ID] = usableVector(r, emb.Space(), 3)
	}
	if reasons["a"] != skipLegacy || reasons["b"] != skipSpace {
		t.Fatalf("each rejection must say why: %v", reasons)
	}
}

func TestReingestWithAStaleVectorDropsItWhenTheNewEmbeddingFails(t *testing.T) {
	srv := embedServer(t, map[string][]float64{}, "FAILME")
	h, s := engineWith(t, srv)
	// A record read back from the store carries the vector of its old text.
	old := stamped([]float32{1, 2, 3}, h.embedder.Space(), "the old text")
	if err := h.Ingest(context.Background(), Record{ID: "r", Content: "FAILME new text", Scope: "s", Modality: "journal", Status: "active", Metadata: old}); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetRecord(context.Background(), "r")
	if err != nil {
		t.Fatal(err)
	}
	if _, has := r.Metadata[metaEmbedding]; has {
		t.Fatal("a vector computed from different text survived a failed re-embedding")
	}
}

func TestQueryDimensionDriftDisablesVectorsWithoutPanicking(t *testing.T) {
	srv := embedServer(t, map[string][]float64{"budget plan": {1, 0}}, "") // gateway now returns 2 coordinates
	emb := NewEmbedder(EmbeddingConfig{APIBase: srv.URL, APIKey: "k", Model: "m1"})
	s := memStore(t)
	putVec(t, s, "a", "budget plan a", stamped([]float32{1, 0, 0}, emb.Space(), "budget plan a"))

	got, err := NewPipeline(s).RecallWithEmbedding(context.Background(), "budget plan", RecallOptions{Scope: "s", Limit: 10}, emb)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", ids(got), err)
	}
	if _, why := usableVector(got[0], emb.Space(), 2); why != skipDimension {
		t.Fatalf("reason = %q", why)
	}
}

func engineWith(t *testing.T, srv *httptest.Server) (*HybridEngine, *SQLiteStore) {
	t.Helper()
	s := memStore(t)
	return &HybridEngine{
		sqlite:   s,
		pipeline: NewPipeline(s),
		embedder: NewEmbedder(EmbeddingConfig{APIBase: srv.URL, APIKey: "k", Model: "m1"}),
	}, s
}

func TestIngestRecordsSpaceDimensionAndTextOfEveryVector(t *testing.T) {
	srv := embedServer(t, map[string][]float64{"hello world": {1, 2, 3}}, "")
	h, s := engineWith(t, srv)
	if err := h.Ingest(context.Background(), Record{ID: "r", Content: "hello world", Scope: "s", Modality: "journal", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetRecord(context.Background(), "r")
	if err != nil {
		t.Fatal(err)
	}
	// What recall sees has been through JSON, so check the round-tripped form.
	if vec, why := usableVector(r, h.embedder.Space(), 3); why != skipNone || len(vec) != 3 || vec[2] != 3 {
		t.Fatalf("vector not usable after storage: %v %q", vec, why)
	}
	if r.Metadata[metaEmbeddingSpace] != h.embedder.Space() {
		t.Fatalf("space = %v", r.Metadata[metaEmbeddingSpace])
	}
}

func TestIngestDoesNotWriteIntoTheCallersMetadata(t *testing.T) {
	srv := embedServer(t, map[string][]float64{"hello world": {1, 2, 3}}, "")
	h, _ := engineWith(t, srv)
	mine := map[string]interface{}{"source": "doc"}
	if err := h.Ingest(context.Background(), Record{ID: "r", Content: "hello world", Scope: "s", Modality: "journal", Status: "active", Metadata: mine}); err != nil {
		t.Fatal(err)
	}
	if len(mine) != 1 {
		t.Fatalf("caller's map was modified: %v", mine)
	}
}

func TestEditingARecordInPlaceRetiresItsVector(t *testing.T) {
	srv := embedServer(t, map[string][]float64{"hello world": {1, 2, 3}}, "")
	h, s := engineWith(t, srv)
	ctx := context.Background()
	if err := h.Ingest(ctx, Record{ID: "r", Content: "hello world", Scope: "s", Modality: "journal", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	// The memory console edits content without going through the embedder.
	if err := s.UpdateRecord(ctx, "r", Record{Content: "something else entirely"}); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetRecord(ctx, "r")
	if err != nil {
		t.Fatal(err)
	}
	if _, why := usableVector(r, h.embedder.Space(), 3); why != skipStale {
		t.Fatalf("an edited record kept ranking with its old vector (reason %q)", why)
	}
}

func TestFailedChunkEmbeddingDoesNotInheritThePreviousChunksVector(t *testing.T) {
	first := strings.Repeat("alpha budget line. ", 40)
	second := "FAILME " + strings.Repeat("omega staffing line. ", 40)
	content := first + "\n\n" + second
	if len(content) <= 1200 || len(chunkContent(content, 1200, 200)) < 2 {
		t.Fatalf("fixture must produce several chunks, got %d", len(chunkContent(content, 1200, 200)))
	}
	srv := embedServer(t, map[string][]float64{}, "FAILME")
	h, s := engineWith(t, srv)
	shared := map[string]interface{}{"source": "doc"}
	if err := h.Ingest(context.Background(), Record{ID: "doc", Content: content, Scope: "s", Modality: "journal", Status: "active", Metadata: shared}); err != nil {
		t.Fatal(err)
	}
	if _, leaked := shared[metaEmbedding]; leaked {
		t.Fatal("ingest wrote into the caller's metadata map")
	}
	var withVec, without int
	for i := 0; ; i++ {
		r, err := s.GetRecord(context.Background(), "doc_c"+string(rune('0'+i)))
		if err != nil {
			break
		}
		if _, why := usableVector(r, h.embedder.Space(), 3); why == skipNone {
			withVec++
			if strings.Contains(r.Content, "FAILME") {
				t.Fatal("the chunk whose embedding failed carries a vector")
			}
		} else {
			without++
			if _, has := r.Metadata[metaEmbedding]; has {
				t.Fatalf("chunk %d kept an unusable vector instead of none", i)
			}
		}
	}
	if withVec == 0 || without == 0 {
		t.Fatalf("expected one chunk with a vector and one without, got %d / %d", withVec, without)
	}
}

func TestEmbeddingSpaceTracksModelAndEndpointButNotCredentials(t *testing.T) {
	base := embeddingSpace("m1", "https://gw.example/v1")
	same := []string{"https://GW.example/v1/", "  https://gw.example/v1  ", "https://user:pw@gw.example/v1?key=abc#x"}
	for _, b := range same {
		if got := embeddingSpace("m1", b); got != base {
			t.Errorf("%q should be the same space, got %s want %s", b, got, base)
		}
	}
	for _, other := range []string{embeddingSpace("m2", "https://gw.example/v1"), embeddingSpace("m1", "https://other.example/v1")} {
		if other == base {
			t.Errorf("distinct model or endpoint collapsed into %s", base)
		}
	}
	a := NewEmbedder(EmbeddingConfig{APIBase: "https://gw.example/v1", APIKey: "one", Model: "m1"})
	b := NewEmbedder(EmbeddingConfig{APIBase: "https://gw.example/v1", APIKey: "two", Model: "m1"})
	if a.Space() != b.Space() || strings.Contains(a.Space(), "one") {
		t.Fatalf("credentials leaked into the space: %s", a.Space())
	}
}
