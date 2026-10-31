package memory

import (
	"regexp"
	"strings"
)

// recordHeader is the "- [id:…; kind; evidence] " prefix every rendered record
// line carries. A leaf keeps it (the model needs the id and the evidence label to
// cite and to doubt it), but repeating it for every child inside a branch excerpt
// only spends the node's few tokens on bookkeeping.
var recordHeader = regexp.MustCompile(`^- \[id:[^\]]*\]\s*`)

// excerptLine renders one child inside the structural abstract of its parent: the
// child's own text without the record header, and without repeating the parent's
// topic label when the child belongs to the same topic.
func excerptLine(parentLabel string, child SummaryNode) string {
	text := strings.TrimSpace(recordHeader.ReplaceAllString(strings.TrimSpace(child.Summary), ""))
	if child.Label == parentLabel || child.Label == "" {
		return text
	}
	return child.Label + ": " + text
}
