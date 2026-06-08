package memory

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// RecallRuntimeConfig controls hybrid recall latency and source weighting.
type RecallRuntimeConfig struct {
	Timeout     time.Duration
	FTSWeight   float64
	MilvusWeight float64
	EmbedRerank bool
}

// RecallConfigFromEnv reads AGENTGO_RECALL_* knobs.
func RecallConfigFromEnv() RecallRuntimeConfig {
	ms := envIntOr("AGENTGO_RECALL_TIMEOUT_MS", 800)
	if ms < 100 {
		ms = 100
	}
	ftsw := envFloatOr("AGENTGO_RECALL_FTS_WEIGHT", 0.45)
	mw := envFloatOr("AGENTGO_RECALL_MILVUS_WEIGHT", 0.55)
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
		EmbedRerank:  envBool("AGENTGO_RECALL_EMBED_RERANK"),
	}
}

func envFloatOr(key string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 {
		return def
	}
	return f
}

func envBool(key string) bool {
	v := strings.TrimSpace(os.Getenv(key))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}
