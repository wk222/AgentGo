package workflow

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Field types a custom node can declare for an input or an output.
const (
	FieldString  = "string"
	FieldNumber  = "number"
	FieldInteger = "integer"
	FieldBoolean = "boolean"
	FieldJSON    = "json"
)

func validFieldType(t string) bool {
	switch t {
	case FieldString, FieldNumber, FieldInteger, FieldBoolean, FieldJSON:
		return true
	}
	return false
}

func valueKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case float64, int, int64:
		return "a number"
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	}
	return fmt.Sprintf("%T", v)
}

// coerceValue converts v (a decoded JSON value, or the text of an expanded
// template) to the declared field type. Text is accepted wherever it parses,
// because template expansion only ever yields text.
func coerceValue(typ string, v any) (any, error) {
	switch typ {
	case "", FieldString:
		switch x := v.(type) {
		case nil:
			return "", nil
		case string:
			return x, nil
		case float64, int, int64, bool:
			return stringifyValue(x), nil
		}
		return nil, fmt.Errorf("expected a string, got %s", valueKind(v))

	case FieldNumber:
		switch x := v.(type) {
		case float64:
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return nil, fmt.Errorf("expected a finite number")
			}
			return x, nil
		case int:
			return float64(x), nil
		case int64:
			return float64(x), nil
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return nil, fmt.Errorf("expected a number, got %q", truncateText(x, 60))
			}
			return f, nil
		}
		return nil, fmt.Errorf("expected a number, got %s", valueKind(v))

	case FieldInteger:
		switch x := v.(type) {
		case float64:
			if x != math.Trunc(x) || math.Abs(x) > 9e15 {
				return nil, fmt.Errorf("expected an integer, got %v", x)
			}
			return int64(x), nil
		case int:
			return int64(x), nil
		case int64:
			return x, nil
		case string:
			s := strings.TrimSpace(x)
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				return n, nil
			}
			if f, err := strconv.ParseFloat(s, 64); err == nil && f == math.Trunc(f) && math.Abs(f) <= 9e15 {
				return int64(f), nil
			}
			return nil, fmt.Errorf("expected an integer, got %q", truncateText(x, 60))
		}
		return nil, fmt.Errorf("expected an integer, got %s", valueKind(v))

	case FieldBoolean:
		switch x := v.(type) {
		case bool:
			return x, nil
		case string:
			switch strings.ToLower(strings.TrimSpace(x)) {
			case "true", "yes", "1", "on":
				return true, nil
			case "false", "no", "0", "off":
				return false, nil
			}
			return nil, fmt.Errorf("expected a boolean, got %q", truncateText(x, 60))
		}
		return nil, fmt.Errorf("expected a boolean, got %s", valueKind(v))

	case FieldJSON:
		if s, ok := v.(string); ok {
			var out any
			if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &out); err != nil {
				return nil, fmt.Errorf("expected JSON text, got %q", truncateText(s, 60))
			}
			return out, nil
		}
		return v, nil
	}
	return nil, fmt.Errorf("unknown field type %q", typ)
}

// stringifyValue is the text form of a value: what a template placeholder, a
// workflow variable and an equality check see.
func stringifyValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// extractJSONObject finds the JSON object a backend printed: the whole text, a
// fenced block, or the last line (a script may print logs before its result).
func extractJSONObject(raw string) (map[string]any, bool) {
	try := func(s string) (map[string]any, bool) {
		s = strings.TrimSpace(s)
		if !strings.HasPrefix(s, "{") {
			return nil, false
		}
		var m map[string]any
		if json.Unmarshal([]byte(s), &m) != nil || m == nil {
			return nil, false
		}
		return m, true
	}
	if m, ok := try(raw); ok {
		return m, true
	}
	if i := strings.Index(raw, "```"); i >= 0 {
		rest := raw[i+3:]
		if j := strings.Index(rest, "```"); j >= 0 {
			block := rest[:j]
			if nl := strings.IndexByte(block, '\n'); nl >= 0 && !strings.Contains(block[:nl], "{") {
				block = block[nl+1:]
			}
			if m, ok := try(block); ok {
				return m, true
			}
		}
	}
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		return try(lines[i])
	}
	return nil, false
}

var inRefPattern = regexp.MustCompile(`\{\{\s*in\.([A-Za-z][A-Za-z0-9_]*)\s*\}\}`)

// nodeEnv carries what workflow templates can reference while a custom node runs.
type nodeEnv struct {
	Input, Last string
	Vars        map[string]string
}

// expandAny walks v and expands templates in every string: {{input}}, {{last}}
// and {{var.x}} first, then {{in.NAME}} from the node's bound inputs (a string
// that is exactly one {{in.NAME}} keeps the input's own type). Doing the inputs
// last means data inside an input is never scanned for variable references.
// A nil inputs map skips {{in.*}}.
func expandAny(v any, inputs map[string]any, env nodeEnv) any {
	switch x := v.(type) {
	case string:
		s := expandTemplateVars(x, env.Input, env.Last, env.Vars)
		if inputs == nil {
			return s
		}
		if m := inRefPattern.FindStringSubmatch(s); m != nil && strings.TrimSpace(s) == m[0] {
			if val, ok := inputs[m[1]]; ok {
				return val
			}
		}
		return inRefPattern.ReplaceAllStringFunc(s, func(ref string) string {
			name := inRefPattern.FindStringSubmatch(ref)[1]
			if val, ok := inputs[name]; ok {
				return stringifyValue(val)
			}
			return ref
		})
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = expandAny(e, inputs, env)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = expandAny(e, inputs, env)
		}
		return out
	}
	return v
}

// walkStrings calls f for every string inside v.
func walkStrings(v any, f func(string)) {
	switch x := v.(type) {
	case string:
		f(x)
	case map[string]any:
		for _, e := range x {
			walkStrings(e, f)
		}
	case []any:
		for _, e := range x {
			walkStrings(e, f)
		}
	}
}
