package memory

import (
	"fmt"
	"strings"
)

// NormalizeAndValidateLayer normalizes taxonomy fields in-place, then checks
// record shape before ingest (PyBot validate_layer subset).
func NormalizeAndValidateLayer(rec *Record) error {
	if rec == nil {
		return fmt.Errorf("memory: nil record")
	}
	if err := ApplyTaxonomy(rec); err != nil {
		return err
	}
	if strings.TrimSpace(rec.Content) == "" {
		return fmt.Errorf("memory: empty content")
	}
	if len(rec.Content) > 512_000 {
		return fmt.Errorf("memory: content exceeds 512KB")
	}
	if strings.TrimSpace(rec.Scope) == "" {
		return fmt.Errorf("memory: scope required")
	}
	layer, _ := rec.Metadata["taxonomy_layer"].(string)
	switch layer {
	case LayerEpisodic, LayerJournal, LayerReflection, LayerInsight, LayerFact:
		return validateLayerSemantics(*rec, layer)
	default:
		return fmt.Errorf("memory: unknown taxonomy layer %q", layer)
	}
}

func validateLayerSemantics(rec Record, layer string) error {
	content := strings.TrimSpace(rec.Content)
	switch layer {
	case LayerJournal, LayerReflection:
		if len(content) < 8 {
			return fmt.Errorf("memory: %s content too short (min 8 runes)", layer)
		}
	case LayerInsight:
		if len(content) < 16 {
			return fmt.Errorf("memory: insight content too short (min 16 runes)")
		}
	case LayerFact:
		if len(content) < 4 {
			return fmt.Errorf("memory: fact content too short")
		}
	}
	if cv, ok := rec.Metadata["execution_canvas"].(string); ok && strings.TrimSpace(cv) != "" {
		switch strings.ToLower(strings.TrimSpace(cv)) {
		case "focused", "balanced", "deep":
		default:
			return fmt.Errorf("memory: invalid execution_canvas %q", cv)
		}
	}
	if mp, ok := rec.Metadata["mode_profile"].(string); ok && strings.TrimSpace(mp) != "" {
		switch strings.ToLower(strings.TrimSpace(mp)) {
		case "assistant", "app_matrix", "admin":
		default:
			return fmt.Errorf("memory: invalid mode_profile %q", mp)
		}
	}
	if imp := rec.Importance; imp < 0 || imp > 10 {
		return fmt.Errorf("memory: importance %v out of range [0,10]", imp)
	}
	return nil
}

// ValidateLayer keeps the value-based validation API for tests and callers that
// only need a shape check.
func ValidateLayer(rec Record) error {
	return NormalizeAndValidateLayer(&rec)
}
