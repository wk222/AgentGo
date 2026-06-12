package memory

import "testing"

func TestValidateLayerRejectsEmpty(t *testing.T) {
	err := ValidateLayer(Record{Content: "", Scope: "s1"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateLayerOK(t *testing.T) {
	err := ValidateLayer(Record{Content: "fact", Scope: "s1", Modality: "fact"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeAndValidateLayerMutatesRecord(t *testing.T) {
	rec := Record{Content: "user turn", Scope: "s1", Modality: "episodic"}
	if err := NormalizeAndValidateLayer(&rec); err != nil {
		t.Fatal(err)
	}
	if rec.Modality != "episode" {
		t.Fatalf("expected normalized modality episode, got %q", rec.Modality)
	}
	if rec.Metadata["taxonomy_layer"] != LayerEpisodic {
		t.Fatalf("expected taxonomy layer %q, got %+v", LayerEpisodic, rec.Metadata)
	}
	if rec.Status != "active" {
		t.Fatalf("expected default active status, got %q", rec.Status)
	}
}

func TestValidateLayerCanvasMetadata(t *testing.T) {
	err := ValidateLayer(Record{
		Content: "hello world", Scope: "s1", Modality: "episode",
		Metadata: map[string]interface{}{"execution_canvas": "focused"},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = ValidateLayer(Record{
		Content: "hello world", Scope: "s1", Modality: "episode",
		Metadata: map[string]interface{}{"execution_canvas": "invalid"},
	})
	if err == nil {
		t.Fatal("expected invalid canvas error")
	}
}
