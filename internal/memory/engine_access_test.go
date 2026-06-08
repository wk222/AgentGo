package memory

import "testing"

func TestPipelineFromEngine(t *testing.T) {
	store, err := NewSQLiteStore("file:pipeline_engine_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHybridEngine(t.Context(), store, BootConfig{})
	if err != nil {
		t.Fatal(err)
	}
	enriched := NewEnrichedEngine(h)
	if PipelineFromEngine(enriched) == nil {
		t.Fatal("expected pipeline from enriched engine")
	}
	if PipelineFromEngine(h) == nil {
		t.Fatal("expected pipeline from hybrid engine")
	}
}
