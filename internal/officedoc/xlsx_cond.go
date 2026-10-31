package officedoc

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type aCond struct {
	Col   string `json:"col" jsonschema:"required,description=Column name as in the header."`
	Op    string `json:"op" jsonschema:"required,description=One of: =\\, !=\\, >\\, >=\\, <\\, <=\\, in (the value is a list)\\, contains (text)\\, blank\\, notblank."`
	Value any    `json:"value,omitempty" jsonschema:"description=Number or text (a list for in). Percent columns are in percentage points: 96 means 96%. A date or month column takes 2026-09 or 2026/9 or 2026年9月 or 9月."`
}

var condOps = map[string]string{
	"=": "eq", "==": "eq", "eq": "eq", "equals": "eq",
	"!=": "ne", "<>": "ne", "ne": "ne", "neq": "ne",
	">": "gt", "gt": "gt", ">=": "ge", "gte": "ge", "ge": "ge",
	"<": "lt", "lt": "lt", "<=": "le", "lte": "le", "le": "le",
	"in": "in", "contains": "contains", "like": "contains",
	"blank": "blank", "isblank": "blank", "empty": "blank",
	"notblank": "notblank", "not_blank": "notblank", "nonblank": "notblank", "notempty": "notblank",
}

// datePat is a possibly partial date: 2026-09 has no day, 9月 has no year.
type datePat struct{ y, m, d int }

var (
	dpFull    = regexp.MustCompile(`^(\d{4})\s*[-/.年]\s*(\d{1,2})(?:\s*[-/.月]\s*(\d{1,2})\s*日?)?\s*月?(?:份)?$`)
	dpMonth   = regexp.MustCompile(`^(\d{1,2})\s*月(?:份)?$`)
	dpCompact = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})?$`)
	dpYear    = regexp.MustCompile(`^(\d{4})\s*年?$`)
)

func parseDatePat(s string) (datePat, bool) {
	s = strings.TrimSpace(s)
	var m []string
	var p datePat
	switch {
	case dpFull.MatchString(s):
		m = dpFull.FindStringSubmatch(s)
		p.y, p.m = atoi(m[1]), atoi(m[2])
		p.d = atoi(m[3])
	case dpMonth.MatchString(s):
		p.m = atoi(dpMonth.FindStringSubmatch(s)[1])
	case dpCompact.MatchString(s):
		m = dpCompact.FindStringSubmatch(s)
		p.y, p.m, p.d = atoi(m[1]), atoi(m[2]), atoi(m[3])
	case dpYear.MatchString(s):
		p.y = atoi(dpYear.FindStringSubmatch(s)[1])
	default:
		return p, false
	}
	if p.m > 12 || (p.m == 0 && p.d > 0) || p.d > 31 {
		return p, false
	}
	return p, true
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func (p datePat) match(t time.Time) bool {
	return (p.y == 0 || t.Year() == p.y) && (p.m == 0 || int(t.Month()) == p.m) && (p.d == 0 || t.Day() == p.d)
}

func (p datePat) start() time.Time {
	return time.Date(p.y, time.Month(max(p.m, 1)), max(p.d, 1), 0, 0, 0, 0, time.UTC)
}

func (p datePat) end() time.Time {
	s := p.start()
	switch {
	case p.d > 0:
		return s.AddDate(0, 0, 1)
	case p.m > 0:
		return s.AddDate(0, 1, 0)
	}
	return s.AddDate(1, 0, 0)
}

// cond is a compiled condition on one column.
type cond struct {
	op   string
	kind cellKind
	nums []float64
	strs []string
	pats []datePat
}

func asList(v any) []any {
	switch x := v.(type) {
	case nil:
		return nil
	case []any:
		return x
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out
	}
	return []any{v}
}

func valueText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return formatNumber(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case bool:
		return strings.ToUpper(strconv.FormatBool(x))
	}
	return fmt.Sprint(v)
}

func valueNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		n, _, _, ok := looseNumber(x)
		return n, ok
	}
	return 0, false
}

// normText makes text comparison forgiving: case, spaces and full-width forms.
func normText(s string) string {
	s = strings.NewReplacer(" ", "", "\u00a0", "", "\u3000", "", "（", "(", "）", ")", "％", "%", "：", ":").Replace(s)
	return strings.ToLower(strings.TrimSpace(s))
}

func compileCond(cd aCond, kind cellKind) (*cond, error) {
	op, ok := condOps[strings.ToLower(strings.TrimSpace(cd.Op))]
	if !ok {
		return nil, fmt.Errorf("unknown op %q (use =, !=, >, >=, <, <=, in, contains, blank, notblank)", cd.Op)
	}
	c := &cond{op: op, kind: kind}
	if op == "blank" || op == "notblank" {
		return c, nil
	}
	vals := asList(cd.Value)
	if len(vals) == 0 {
		return nil, fmt.Errorf("op %q on %q needs a value", cd.Op, cd.Col)
	}
	if op == "eq" && len(vals) > 1 {
		c.op = "in"
	}
	if len(vals) > 1 && c.op != "in" {
		return nil, fmt.Errorf("op %q takes one value; use in for a list", cd.Op)
	}
	for _, v := range vals {
		c.strs = append(c.strs, normText(valueText(v)))
		switch {
		case kind == ckNum && op != "contains":
			n, ok := valueNumber(v)
			if !ok {
				return nil, fmt.Errorf("column %q is numeric but %q is not a number", cd.Col, valueText(v))
			}
			c.nums = append(c.nums, n)
		case kind == ckDate && op != "contains":
			p, ok := parseDatePat(valueText(v))
			if n, isNum := valueNumber(v); !ok && isNum && n == math.Trunc(n) && n >= 1 && n <= 12 {
				p, ok = datePat{m: int(n)}, true // a bare 9 means September
			}
			if !ok {
				return nil, fmt.Errorf("column %q holds dates but %q is not one (use 2026-09, 2026/9/15, 2026年9月 or 9月)", cd.Col, valueText(v))
			}
			c.pats = append(c.pats, p)
		default:
			if p, ok := parseDatePat(valueText(v)); ok {
				c.pats = append(c.pats, p)
			} else {
				c.pats = append(c.pats, datePat{})
			}
		}
	}
	if kind == ckDate && (op == "gt" || op == "ge" || op == "lt" || op == "le") && c.pats[0].y == 0 {
		return nil, fmt.Errorf("comparing dates with %q needs a year, such as 2026-06", cd.Op)
	}
	return c, nil
}

// match applies the condition to one cell. A blank cell fails every test
// except blank and !=.
func (c *cond) match(cell aCell) bool {
	switch c.op {
	case "blank":
		return cell.kind == ckBlank
	case "notblank":
		return cell.kind != ckBlank
	case "ne":
		return !c.equal(cell)
	case "eq", "in":
		return c.equal(cell)
	case "contains":
		return cell.kind != ckBlank && strings.Contains(normText(aText(cell)), c.strs[0])
	}
	// ordering
	switch cell.kind {
	case ckNum:
		if c.kind != ckNum {
			return false
		}
		v := c.nums[0]
		return orderOK(c.op, cell.n < v, cell.n > v)
	case ckDate:
		if c.kind != ckDate {
			return false
		}
		p := c.pats[0]
		switch c.op {
		case "ge":
			return !cell.t.Before(p.start())
		case "gt":
			return !cell.t.Before(p.end())
		case "lt":
			return cell.t.Before(p.start())
		default:
			return cell.t.Before(p.end())
		}
	case ckText:
		cmp := strings.Compare(normText(cell.s), c.strs[0])
		return orderOK(c.op, cmp < 0, cmp > 0)
	}
	return false
}

func orderOK(op string, less, greater bool) bool {
	switch op {
	case "gt":
		return greater
	case "ge":
		return !less
	case "lt":
		return less
	}
	return !greater
}

func (c *cond) equal(cell aCell) bool {
	switch cell.kind {
	case ckNum:
		for _, v := range c.nums {
			if math.Abs(cell.n-v) <= 1e-9*math.Max(1, math.Abs(v)) {
				return true
			}
		}
	case ckDate:
		for _, p := range c.pats {
			if p.y+p.m+p.d > 0 && p.match(cell.t) {
				return true
			}
		}
		for _, s := range c.strs {
			if normText(cell.s) == s {
				return true
			}
		}
	case ckText:
		n := normText(cell.s)
		cp, cok := parseDatePat(cell.s)
		for i, s := range c.strs {
			if n == s {
				return true
			}
			// "2026年9月" and "2026-09" name the same month.
			if i >= len(c.pats) {
				continue
			}
			if p := c.pats[i]; cok && p.y > 0 && p.m > 0 && cp.y == p.y && cp.m == p.m && (p.d == 0 || cp.d == 0 || p.d == cp.d) {
				return true
			}
		}
	}
	return false
}

// findExact resolves a name exactly, then ignoring case, spaces and
// full-width brackets.
func findExact(names []string, want string) (int, bool) {
	for i, n := range names {
		if n == want {
			return i, true
		}
	}
	w := normText(want)
	if w == "" {
		return -1, false
	}
	for i, n := range names {
		if normText(n) == w {
			return i, true
		}
	}
	return -1, false
}

// findLoose resolves a name that is part of exactly one candidate; ambiguous
// lists the candidates when several fit.
func findLoose(names []string, want string) (idx int, ambiguous []string, ok bool) {
	w := normText(want)
	if w == "" {
		return -1, nil, false
	}
	var hits []int
	for i, n := range names {
		if strings.Contains(normText(n), w) {
			hits = append(hits, i)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil, true
	case 0:
		return -1, nil, false
	}
	for _, i := range hits {
		ambiguous = append(ambiguous, names[i])
	}
	return -1, ambiguous, false
}

// findName is findExact then findLoose.
func findName(names []string, want string) (idx int, ambiguous []string, ok bool) {
	if i, ok := findExact(names, want); ok {
		return i, nil, true
	}
	return findLoose(names, want)
}
func nameList(names []string) string {
	return strings.Join(limitStrings(names, 40), ", ")
}

// colIndex finds a source column by header name, by letter (C, AB), or by a
// part of the header that fits only one column.
func (t *aTable) colIndex(want string) (int, error) {
	names := make([]string, len(t.cols))
	for i, c := range t.cols {
		names[i] = c.name
	}
	if i, ok := findExact(names, want); ok {
		return i, nil
	}
	if l := strings.ToUpper(strings.TrimSpace(want)); l != "" && len(l) <= 3 && strings.Trim(l, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") == "" {
		for i, c := range t.cols {
			if c.letter == l {
				return i, nil
			}
		}
	}
	i, amb, ok := findLoose(names, want)
	if ok {
		return i, nil
	}
	if len(amb) > 0 {
		return -1, fmt.Errorf("column %q is ambiguous: %s", want, nameList(amb))
	}
	return -1, fmt.Errorf("no column %q; columns: %s", want, nameList(names))
}
