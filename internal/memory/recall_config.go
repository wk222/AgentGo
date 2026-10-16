package memory

import (
	"time"

	"agentgo/internal/config"
)

// RecallRuntimeConfig controls hybrid recall latency and source weighting.
type RecallRuntimeConfig struct {
	Timeout      time.Duration
	FTSWeight    float64
	MilvusWeight float64
	EmbedRerank  bool
}

// RecallConfigFromEnv reads AGENTGO_RECALL_* knobs.
func RecallConfigFromEnv() RecallRuntimeConfig {
	ms := config.RecallTimeoutMS.Get()
	if ms < 100 {
		ms = 100
	}
	ftsw, mw := config.RecallFTSWeight.Get(), config.RecallMilvusW.Get()
	if ftsw < 0 {
		ftsw = 0.45
	}
	if mw < 0 {
		mw = 0.55
	}
	sum := ftsw + mw
	if sum <= 0 {
		ftsw, mw = 0.45, 0.55
	} else {
		ftsw /= sum
		mw /= sum
	}
	return RecallRuntimeConfig{
		Timeout:      time.Duration(ms) * time.Millisecond,
		FTSWeight:    ftsw,
		MilvusWeight: mw,
		EmbedRerank:  config.RecallEmbedRerank.On(),
	}
}
