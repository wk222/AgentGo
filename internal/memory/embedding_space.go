package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// A stored embedding is only comparable with a query vector from the same
// embedding space. Equal length is not evidence of that: two models can share a
// dimension and still produce unrelated coordinates, and a vector keeps
// ranking after the text it was computed from has been edited. So every vector
// is written with the space it came from and a hash of the text it embeds, and
// recall ignores any vector that cannot prove both.
const (
	metaEmbedding      = "embedding"
	metaEmbeddingSpace = "embedding_space"
	metaEmbeddingDim   = "embedding_dim"
	metaEmbeddingSum   = "embedding_sha"
)

// Space names the embedding space this Embedder writes into: model plus
// endpoint. Credentials never take part. Two gateways serving the same model
// name are treated as different spaces, because a gateway may route one name to
// different models.
func (e *Embedder) Space() string {
	if e == nil {
		return ""
	}
	return embeddingSpace(e.cfg.Model, e.cfg.APIBase)
}

func embeddingSpace(model, apiBase string) string {
	base := strings.ToLower(strings.TrimSpace(apiBase))
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		u.User, u.RawQuery, u.Fragment = nil, "", ""
		base = u.String()
	}
	base = strings.TrimRight(base, "/")
	sum := sha256.Sum256([]byte(base))
	return strings.TrimSpace(model) + "@" + hex.EncodeToString(sum[:])[:8]
}

func embeddingTextSum(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:16]
}

// stampEmbedding writes vec and the facts needed to trust it later. meta must
// be owned by the caller.
func stampEmbedding(meta map[string]interface{}, vec []float32, space, text string) {
	meta[metaEmbedding] = vec
	meta[metaEmbeddingSpace] = space
	meta[metaEmbeddingDim] = len(vec)
	meta[metaEmbeddingSum] = embeddingTextSum(text)
}

func clearEmbedding(meta map[string]interface{}) {
	delete(meta, metaEmbedding)
	delete(meta, metaEmbeddingSpace)
	delete(meta, metaEmbeddingDim)
	delete(meta, metaEmbeddingSum)
}

// Why a stored vector was not used.
const (
	skipNone      = ""
	skipNoVector  = "no_vector"
	skipLegacy    = "unlabeled"
	skipSpace     = "other_space"
	skipDimension = "dimension"
	skipStale     = "text_changed"
)

// usableVector returns the record's vector only when it was computed by the
// same space as the query, with the query's dimension, from the text the
// record holds now.
func usableVector(r Record, space string, queryDim int) ([]float32, string) {
	vec := vectorFromMeta(r.Metadata[metaEmbedding])
	if len(vec) == 0 {
		return nil, skipNoVector
	}
	got, _ := r.Metadata[metaEmbeddingSpace].(string)
	if got == "" {
		return nil, skipLegacy
	}
	if got != space {
		return nil, skipSpace
	}
	if len(vec) != queryDim || embeddingDimOf(r.Metadata[metaEmbeddingDim]) != len(vec) {
		return nil, skipDimension
	}
	if sum, _ := r.Metadata[metaEmbeddingSum].(string); sum != embeddingTextSum(r.Content) {
		return nil, skipStale
	}
	return vec, skipNone
}

func embeddingDimOf(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case float64: // after a JSON round trip
		return int(n)
	}
	return -1
}

func vectorFromMeta(v interface{}) []float32 {
	switch vec := v.(type) {
	case []float32:
		return vec
	case []interface{}:
		out := make([]float32, len(vec))
		for i, x := range vec {
			f, ok := x.(float64)
			if !ok {
				return nil
			}
			out[i] = float32(f)
		}
		return out
	}
	return nil
}

var lastSkipLog atomic.Int64

// reportSkippedVectors says, at most once a minute, that vectors were left out
// of ranking and why. Silent exclusion would look like a recall regression
// with no explanation.
func reportSkippedVectors(skipped map[string]int, space string) {
	total := 0
	for reason, n := range skipped {
		if reason != skipNoVector {
			total += n
		}
	}
	if total == 0 {
		return
	}
	now := time.Now().Unix()
	if last := lastSkipLog.Load(); now-last < 60 || !lastSkipLog.CompareAndSwap(last, now) {
		return
	}
	var parts []string
	for _, reason := range []string{skipLegacy, skipSpace, skipDimension, skipStale} {
		if n := skipped[reason]; n > 0 {
			parts = append(parts, reason+"="+strconv.Itoa(n))
		}
	}
	log.Printf("[memory] %d stored vector(s) not used for ranking in space %s (%s); full-text recall is unaffected", total, space, strings.Join(parts, ", "))
}
