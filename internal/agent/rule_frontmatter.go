package agent

import "strings"

// ruleMeta is the front matter of a Cursor rule file (.cursor/rules/*.mdc).
type ruleMeta struct {
	Present     bool // the file starts with a front matter block
	Description string
	Globs       []string
	AlwaysApply *bool // nil when the key is absent
}

// parseRuleFile splits a rule file into its front matter and its body. Cursor
// writes front matter that is not valid YAML (a bare glob such as *.ts begins
// with the YAML alias marker), so the three keys that matter are read line by
// line instead of handing the block to a YAML parser. A file without a closed
// front matter block is all body.
func parseRuleFile(text string) (ruleMeta, string) {
	text = strings.TrimPrefix(text, "\ufeff")
	lines := strings.Split(text, "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		return ruleMeta{}, text
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return ruleMeta{}, text
	}

	meta := ruleMeta{Present: true}
	inGlobList := false
	for _, raw := range lines[1:end] {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if item, ok := strings.CutPrefix(line, "-"); ok { // an item of the list the key above opened
			if inGlobList {
				if glob := unquoteRuleValue(item); glob != "" {
					meta.Globs = append(meta.Globs, glob)
				}
			}
			continue
		}
		inGlobList = false
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "description":
			meta.Description = unquoteRuleValue(value)
		case "globs":
			meta.Globs = append(meta.Globs, splitRuleGlobs(value)...)
			inGlobList = value == ""
		case "alwaysapply":
			switch strings.ToLower(unquoteRuleValue(value)) {
			case "true", "yes", "on":
				on := true
				meta.AlwaysApply = &on
			case "false", "no", "off":
				off := false
				meta.AlwaysApply = &off
			}
		}
	}
	return meta, strings.TrimLeft(strings.Join(lines[end+1:], "\n"), "\r\n")
}

// splitRuleGlobs reads the inline forms of the globs value: "a, b" and "[a, b]".
func splitRuleGlobs(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	var globs []string
	for _, part := range strings.Split(value, ",") {
		if glob := unquoteRuleValue(part); glob != "" {
			globs = append(globs, glob)
		}
	}
	return globs
}

func unquoteRuleValue(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	}
	return strings.TrimSpace(s)
}
