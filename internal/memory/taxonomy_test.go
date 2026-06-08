package memory

import "testing"

func TestApplyTaxonomy(t *testing.T) {
	rec := Record{Modality: "episode"}
	if err := ApplyTaxonomy(&rec); err != nil {
		t.Fatal(err)
	}
	if rec.Metadata["taxonomy_layer"] != LayerEpisodic {
		t.Fatalf("layer=%v", rec.Metadata["taxonomy_layer"])
	}
}
