package externalcontent

import (
	"regexp"
	"strings"
)

var (
	injectMarkers = []string{
		"ignore previous", "ignore all previous", "disregard prior",
		"system prompt", "you are now", "new instructions:",
		"### instruction", "<|im_start|>",
	}
	htmlScriptRe = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
)

// Sanitize strips common prompt-injection patterns from untrusted external text (PyBot external_content).
func Sanitize(raw string, maxLen int) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = htmlScriptRe.ReplaceAllString(s, "")
	lower := strings.ToLower(s)
	for _, m := range injectMarkers {
		if strings.Contains(lower, m) {
			s = "[external content redacted: injection pattern]\n" + truncateSafe(s, 200)
			break
		}
	}
	if maxLen <= 0 {
		maxLen = 32_000
	}
	return truncateSafe(s, maxLen)
}

// Wrap formats sanitized content for model injection.
func Wrap(source, body string) string {
	body = Sanitize(body, 0)
	if body == "" {
		return ""
	}
	return "[External Content from " + source + "]\n" + body
}

func truncateSafe(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
