// Package docchunk splits document text into retrieval-sized chunks.
//
// Derived from WeKnora (github.com/Tencent/WeKnora), MIT. See
// licenses/weknora/NOTICE.md. The original is
// WeKnora's internal/infrastructure/chunker; it was ported here so AgentGo can
// use it without pulling in WeKnora's logger, image pipeline or jieba tokenizer.
//
// The package is deliberately self-contained: standard library only, so it can
// be dropped from a build with a build tag (Win7) without dragging anything
// else along.
//
// What it adds over a plain "split on blank lines" chunker:
//
//   - Strategy tiers (heading / heuristic / recursive / legacy) chosen from a
//     document profile, with a validator that rejects a tier's output and falls
//     through to the next one.
//   - Protected spans: fenced code, inline code, tables, LaTeX block math and
//     Markdown links/images are kept whole instead of being cut mid-token.
//   - Heading breadcrumbs (Chunk.ContextHeader) so a chunk carries the section
//     path it came from without polluting its own byte offsets.
//   - Parent/child splitting: large parent chunks for context, small children
//     for embedding.
//   - Rune offsets (Chunk.Start/End) that reconstruct the source exactly, which
//     is what makes page-level citation possible.
package docchunk

import "log"

// debugLogging turns on tier-rejection logging. Off by default: the rejection is
// already visible in Diagnostics, and a chat turn should not write to the log
// for a document that simply took the legacy tier.
var debugLogging bool

// SetDebug enables or disables tier-rejection logging.
func SetDebug(on bool) { debugLogging = on }

func debugf(format string, args ...any) {
	if debugLogging {
		log.Printf(format, args...)
	}
}
