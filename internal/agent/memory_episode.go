package agent

import (
	"fmt"
	"strings"
	"time"

	"agentgo/internal/memory"
)

const (
	minEpisodeRunes = 20
	maxEpisodeRunes = 4000
)

// assistantEpisodeRecord turns a final assistant reply into a memory episode.
// It is always labelled as an unverified assistant report; trivial replies are
// skipped and oversized ones are bounded so one answer cannot dominate recall.
func assistantEpisodeRecord(scope, text, source string, extra map[string]interface{}) (memory.Record, bool) {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) < minEpisodeRunes {
		return memory.Record{}, false
	}
	meta := map[string]interface{}{"source": source, "verification": "assistant_report_unverified"}
	for k, v := range extra {
		meta[k] = v
	}
	if len(runes) > maxEpisodeRunes {
		text = string(runes[:maxEpisodeRunes/2]) + "\n…[reply truncated for memory]…\n" + string(runes[len(runes)-maxEpisodeRunes/2:])
		meta["truncated"] = true
	}
	return memory.Record{
		ID:          fmt.Sprintf("turn_%d", time.Now().UnixNano()),
		Content:     text,
		Scope:       scope,
		Modality:    "episode",
		SourceTrust: 0.35,
		Status:      "active",
		Metadata:    meta,
	}, true
}
