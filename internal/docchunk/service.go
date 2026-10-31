package docchunk

import "strings"

// Service adapts the splitter to the shapes other packages need: a chunker the
// memory hook can call, and a strategy name for logging and diagnostics.
//
// It exists so a plugin can provide one value on the service bus instead of
// wiring individual functions, and so callers get a nil-safe object: a
// zero-value Service uses the default strategy.
type Service struct {
	strategy string
}

// Source is the plugin/service name used in audit and capability records.
const Source = "docchunk"

// NewService returns a Service bound to strategy. An empty or unknown strategy
// falls back to auto, which lets the document profile choose a tier.
func NewService(strategy string) *Service {
	s := strings.ToLower(strings.TrimSpace(strategy))
	switch s {
	case StrategyAuto, StrategyHeading, StrategyHeuristic, StrategyRecursive, StrategyLegacy:
	default:
		s = StrategyAuto
	}
	return &Service{strategy: s}
}

// Strategy reports the effective tier hint.
func (s *Service) Strategy() string {
	if s == nil || s.strategy == "" {
		return StrategyAuto
	}
	return s.strategy
}

// Chunk splits content into at most maxChunkSize runes per chunk. It matches
// memory.ChunkFunc so the memory package can call it through its hook without
// importing this package.
//
// The heading breadcrumb the splitter produces is folded into each chunk's text
// here. memory stores chunk text verbatim and its recall is FTS-first, so a
// chunk that says only "7 天内可申请" is much harder to find than one that also
// says which section it came from.
func (s *Service) Chunk(content string, maxChunkSize, overlap int) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	if maxChunkSize <= 0 {
		maxChunkSize = DefaultChunkSize
	}
	if len([]rune(content)) <= maxChunkSize {
		return []string{content}
	}
	chunks := Split(content, SplitterConfig{
		ChunkSize:    maxChunkSize,
		ChunkOverlap: overlap,
		Separators:   []string{"\n\n", "\n", "。"},
		Strategy:     s.Strategy(),
	})
	out := make([]string, 0, len(chunks))
	for _, ch := range chunks {
		text := strings.TrimSpace(ch.EmbeddingContent())
		if text != "" {
			out = append(out, text)
		}
	}
	if len(out) == 0 {
		return []string{content}
	}
	return out
}
