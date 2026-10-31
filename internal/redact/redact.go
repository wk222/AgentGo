// Package redact hides credentials and the Windows account name in text that leaves the process
// (diagnostics bundles, call traces). Standard library only.
package redact

import "regexp"

var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	// OpenAI-style keys, wherever they show up (the boundary keeps "task-runner-config" and "disk-usage-report" alone).
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{10,}`), `sk-***`},
	// Authorization headers.
	{regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=\-]{8,}`), `${1}***`},
	// "api_key": "value", token='value', PASSWORD = "value" (quoted values: JSON and most log formats).
	{regexp.MustCompile(`(?i)((?:api[_-]?key|apikey|secret|token|password|passwd|authorization|access[_-]?key)["']?\s*[:=]\s*)["'][^"'\r\n]{4,}["']`), `${1}"***"`},
	// AZURE_OPENAI_API_KEY=value (unquoted). Eight characters or more, so true/false/null and small numbers stay.
	{regexp.MustCompile(`(?i)((?:api[_-]?key|apikey|secret|token|password|passwd|access[_-]?key)\s*[:=]\s*)[^\s"',;}&)]{8,}`), `${1}***`},
	// Tokens in URLs: the web build's access link carries ?t=<token>.
	{regexp.MustCompile(`(?i)([?&](?:t|token|key|api_key|access_token|sig|signature)=)[^&\s"']+`), `${1}***`},
	// The Windows account name is a person's name or a staff number.
	// Go prints Windows paths with forward slashes in stack traces, so "/" counts as a separator too.
	{regexp.MustCompile(`(?i)([A-Za-z]:(?:\\\\|\\|/)Users(?:\\\\|\\|/))[^\\/"'\s]+`), `${1}<user>`},
}

// Secrets hides API keys, bearer tokens, key=value credentials, URL tokens and the Windows account name.
func Secrets(s string) string {
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}
