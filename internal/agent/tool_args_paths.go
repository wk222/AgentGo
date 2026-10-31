package agent

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Models routinely write Windows paths into JSON without doubling the
// backslashes. Two different things then happen:
//
//   - C:\Users\user\a.txt is invalid JSON (\U is not an escape), so the call is
//     rejected and the model needs another round trip to fix a path it will
//     likely get wrong again.
//   - C:\temp\new.txt is valid JSON. It silently decodes to C:<TAB>emp<LF>ew.txt,
//     a different path, and the tool then fails on a name nobody asked for.
//
// The first is repaired for every string: an invalid escape cannot have meant
// anything else. The second can only be told apart from deliberate text by the
// field it sits in, so it is repaired only in path-like fields, where a control
// character is never legitimate.

// escapeInvalidBackslashes doubles every backslash inside a JSON string that
// does not start a valid escape sequence. Valid JSON is never passed in.
func escapeInvalidBackslashes(raw string) string {
	var b strings.Builder
	b.Grow(len(raw) + 8)
	inString := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !inString {
			b.WriteByte(c)
			if c == '"' {
				inString = true
			}
			continue
		}
		switch {
		case c == '"':
			inString = false
			b.WriteByte(c)
		case c == '\\' && i+1 < len(raw) && strings.IndexByte(`"\/bfnrt`, raw[i+1]) >= 0:
			b.WriteByte(c)
			b.WriteByte(raw[i+1])
			i++
		case c == '\\' && i+5 < len(raw) && raw[i+1] == 'u' && isHex4(raw[i+2:i+6]):
			b.WriteString(raw[i : i+6])
			i += 5
		case c == '\\':
			b.WriteString(`\\`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isHex4(s string) bool {
	if len(s) != 4 {
		return false
	}
	for i := 0; i < 4; i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// isPathKey reports whether an argument name holds a filesystem path. "source"
// and "target" are deliberately absent: here they are often free text.
func isPathKey(k string) bool {
	k = strings.ToLower(k)
	switch k {
	case "path", "paths", "dir", "directory", "file", "filename", "cwd", "workdir", "folder", "src", "dst", "dest", "destination":
		return true
	}
	return strings.HasSuffix(k, "_path") || strings.HasSuffix(k, "_dir") || strings.HasSuffix(k, "_file")
}

var controlAsEscape = strings.NewReplacer("\t", `\t`, "\n", `\n`, "\r", `\r`, "\b", `\b`, "\f", `\f`)

// restoreWindowsPaths undoes the silent case: in path-like fields, a control
// character that came from \t \n \r \b \f is put back as backslash plus letter.
// Anything that needs no change is returned byte for byte.
func restoreWindowsPaths(raw string) string {
	if !strings.Contains(raw, `\`) {
		return raw
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber() // do not round numbers that are not being touched
	var v any
	if dec.Decode(&v) != nil {
		return raw
	}
	if !restorePathValue(v) {
		return raw
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return raw
	}
	return strings.TrimRight(out.String(), "\n")
}

func restorePathValue(v any) (changed bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if !isPathKey(k) {
				if restorePathValue(val) {
					changed = true
				}
				continue
			}
			switch p := val.(type) {
			case string:
				if fixed := controlAsEscape.Replace(p); fixed != p {
					t[k] = fixed
					changed = true
				}
			case []any: // e.g. "paths": ["C:\temp\a", ...]
				for i, item := range p {
					if s, ok := item.(string); ok {
						if fixed := controlAsEscape.Replace(s); fixed != s {
							p[i] = fixed
							changed = true
						}
					}
				}
			default:
				if restorePathValue(val) {
					changed = true
				}
			}
		}
	case []any:
		for _, item := range t {
			if restorePathValue(item) {
				changed = true
			}
		}
	}
	return changed
}
