package workflow

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"strings"
)

// LLM function names must match ^[a-zA-Z0-9_.-]+$ (OpenAI-compatible APIs reject the whole
// request with a 400 otherwise), so user-facing workflow names (often Chinese, with spaces or
// punctuation) cannot be used verbatim as tool names.
var toolNameUnsafe = regexp.MustCompile(`[^a-z0-9_.]+`)

const toolNameMaxLen = 48

// SafeToolName turns a free-form workflow name into a valid tool-name segment.
//
// Names that are already plain ASCII keep the historical mapping (lower-case, spaces and
// dashes become underscores). Names that needed other substitutions get a short hash of the
// original name appended so that two distinct names never collapse to the same tool. A name
// with no usable characters falls back to fallbackID (or "unnamed").
func SafeToolName(name, fallbackID string) string {
	orig := strings.TrimSpace(name)
	s := strings.ToLower(orig)
	s = strings.NewReplacer(" ", "_", "-", "_").Replace(s)
	changed := toolNameUnsafe.MatchString(s)
	s = strings.Trim(toolNameUnsafe.ReplaceAllString(s, "_"), "_")

	if changed || len(s) > toolNameMaxLen {
		if len(s) > toolNameMaxLen {
			s = strings.TrimRight(s[:toolNameMaxLen], "_")
		}
		sum := sha1.Sum([]byte(orig + "|" + fallbackID))
		suffix := hex.EncodeToString(sum[:])[:8]
		if s == "" {
			return "wf_" + suffix
		}
		return s + "_" + suffix
	}
	if s == "" {
		if id := strings.TrimSpace(fallbackID); id != "" {
			return SafeToolName(id, "")
		}
		return "unnamed"
	}
	return s
}
