package memory

import (
	"fmt"
	"strings"
)

// Taxonomy layers (PyBot four-layer memory taxonomy subset).
const (
	LayerEpisodic   = "episodic"
	LayerJournal    = "journal"
	LayerReflection = "reflection"
	LayerInsight    = "insight"
	LayerFact       = "fact"
)

// ApplyTaxonomy normalizes modality and stamps metadata["taxonomy_layer"].
func ApplyTaxonomy(rec *Record) error {
	if rec == nil {
		return fmt.Errorf("nil record")
	}
	rec.Modality = NormalizeModality(rec.Modality)
	layer := modalityToLayer(rec.Modality)
	if rec.Metadata == nil {
		rec.Metadata = make(map[string]interface{})
	}
	rec.Metadata["taxonomy_layer"] = layer
	if rec.Status == "" {
		rec.Status = "active"
	}
	if rec.Importance <= 0 {
		rec.Importance = defaultImportance(layer)
	}
	return nil
}

// NormalizeModality maps aliases to canonical modality strings.
func NormalizeModality(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "episodic", "episode", "turn":
		return "episode"
	case "journal", "diary":
		return modalityJournal
	case "reflection", "reflect", "distill":
		return modalityReflection
	case "insight", "insights":
		return modalityInsight
	case "fact", "semantic":
		return "fact"
	default:
		if m == "" {
			return "episode"
		}
		return strings.ToLower(strings.TrimSpace(m))
	}
}

func modalityToLayer(modality string) string {
	switch modality {
	case "episode":
		return LayerEpisodic
	case modalityJournal:
		return LayerJournal
	case modalityReflection:
		return LayerReflection
	case modalityInsight:
		return LayerInsight
	case "fact":
		return LayerFact
	default:
		return LayerEpisodic
	}
}

func defaultImportance(layer string) float64 {
	switch layer {
	case LayerInsight:
		return 1.2
	case LayerReflection:
		return 1.0
	case LayerJournal:
		return 0.5
	case LayerFact:
		return 0.8
	default:
		return 0.6
	}
}
