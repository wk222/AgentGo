package memory

// PipelineFromEngine returns the distill/GC pipeline under Hybrid or Enriched engines.
func PipelineFromEngine(e Engine) *Pipeline {
	if e == nil {
		return nil
	}
	switch v := e.(type) {
	case *Pipeline:
		return v
	case *HybridEngine:
		if v != nil {
			return v.Pipeline()
		}
	case *EnrichedEngine:
		if v != nil && v.HybridEngine != nil {
			return v.HybridEngine.Pipeline()
		}
	}
	return nil
}
