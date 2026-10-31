package userhooks

import (
	"fmt"
	"regexp"
	"strings"
)

// matcher picks the tool calls a hook applies to. The rules are the ones Codex
// and Claude Code use, so a matcher written for either means the same here:
//
//   - empty or "*": every tool;
//   - only letters, digits, "_" and "|": the tool's exact name, or one of
//     several ("Bash|Edit");
//   - anything else: a regular expression searched in the name ("code_.*").
type matcher struct {
	all   bool
	exact []string
	re    *regexp.Regexp
}

func compileMatcher(text string) (matcher, error) {
	text = strings.TrimSpace(text)
	if text == "" || text == "*" {
		return matcher{all: true}, nil
	}
	if plainNames(text) {
		var names []string
		for _, n := range strings.Split(text, "|") {
			if n != "" {
				names = append(names, n)
			}
		}
		return matcher{exact: names}, nil
	}
	re, err := regexp.Compile(text)
	if err != nil {
		return matcher{}, fmt.Errorf("is not a valid regular expression: %v", err)
	}
	return matcher{re: re}, nil
}

func plainNames(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '|':
		default:
			return false
		}
	}
	return true
}

// matches reports whether any of names (a tool's own name and the other names it
// is known by) is picked.
func (m matcher) matches(names []string) bool {
	if m.all {
		return true
	}
	for _, name := range names {
		if m.re != nil {
			if m.re.MatchString(name) {
				return true
			}
			continue
		}
		for _, want := range m.exact {
			if name == want {
				return true
			}
		}
	}
	return false
}
