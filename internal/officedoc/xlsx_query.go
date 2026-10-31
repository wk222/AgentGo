package officedoc

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	defaultResultRows = 50
	maxResultRows     = 200
)

type aMetric struct {
	Col    string `json:"col,omitempty" jsonschema:"description=Column to compute on. Leave empty to count rows."`
	Fn     string `json:"fn" jsonschema:"required,description=sum\\, avg\\, min\\, max\\, median\\, count (non-blank cells of col\\, or rows when col is empty)\\, count_distinct\\, first\\, last\\, weighted_avg (needs weight)."`
	As     string `json:"as,omitempty" jsonschema:"description=Name of the result column. Default: the column name (col_fn when the same column has several metrics)."`
	Weight string `json:"weight,omitempty" jsonschema:"description=Weight column for weighted_avg\\, for example sites when averaging a rate over cities."`
}

type aQuery struct {
	Name      string    `json:"name,omitempty" jsonschema:"description=Label echoed in the result."`
	Op        string    `json:"op,omitempty" jsonschema:"description=profile | aggregate (default) | compare | pivot | rows."`
	Where     []aCond   `json:"where,omitempty" jsonschema:"description=Filters on the source columns (all must hold). Filter the period here before summing."`
	GroupBy   []string  `json:"group_by,omitempty" jsonschema:"description=Columns to group by. Empty: one overall row."`
	Metrics   []aMetric `json:"metrics,omitempty" jsonschema:"description=What to compute per group (default: the row count)."`
	PeriodCol string    `json:"period_col,omitempty" jsonschema:"description=compare: the column that holds the period."`
	Current   any       `json:"current,omitempty" jsonschema:"description=compare: the period to report\\, e.g. 2026-09."`
	Previous  any       `json:"previous,omitempty" jsonschema:"description=compare: the period to compare with\\, e.g. 2026-08 or 2025-09."`
	PivotCol  string    `json:"pivot_col,omitempty" jsonschema:"description=pivot: the column whose values become result columns (one metric)."`
	Select    []string  `json:"select,omitempty" jsonschema:"description=rows: the columns to return (default: all\\, up to 15)."`
	Having    []aCond   `json:"having,omitempty" jsonschema:"description=Filters on the RESULT columns after grouping\\, by result column name."`
	OrderBy   string    `json:"order_by,omitempty" jsonschema:"description=Result column to sort by (blanks last)."`
	Desc      bool      `json:"desc,omitempty" jsonschema:"description=Largest first (default ascending)."`
	Limit     int       `json:"limit,omitempty" jsonschema:"description=Maximum result rows (default 50\\, max 200); with order_by this is top or bottom N."`
	Rank      bool      `json:"rank,omitempty" jsonschema:"description=Add a rank column from order_by (equal values share a rank)."`
	Decimals  *int      `json:"decimals,omitempty" jsonschema:"description=Decimal places (default 2)."`
}

type rcol struct {
	name string
	unit string
	kind cellKind
}

// rtab is a query result: named, typed columns of cells.
type rtab struct {
	cols []rcol
	rows [][]aCell
}

type mspec struct {
	col    int // -1 counts rows
	fn     string
	name   string
	weight int
	unit   string
	kind   cellKind
}

var fnAlias = map[string]string{
	"sum": "sum", "求和": "sum", "合计": "sum", "total": "sum",
	"avg": "avg", "mean": "avg", "average": "avg", "平均": "avg", "均值": "avg",
	"min": "min", "最小": "min", "max": "max", "最大": "max",
	"median": "median", "中位数": "median",
	"count": "count", "计数": "count", "数量": "count",
	"count_distinct": "count_distinct", "distinct": "count_distinct", "nunique": "count_distinct", "去重计数": "count_distinct",
	"first": "first", "last": "last",
	"weighted_avg": "weighted_avg", "wavg": "weighted_avg", "加权平均": "weighted_avg",
}

func numCell(v float64, pct bool) aCell { return aCell{kind: ckNum, n: v, pct: pct} }

// filter returns the rows that satisfy every where condition.
func (t *aTable) filter(where []aCond) ([]aRow, []string, error) {
	type test struct {
		col int
		c   *cond
	}
	var tests []test
	for _, w := range where {
		ci, err := t.colIndex(w.Col)
		if err != nil {
			return nil, nil, fmt.Errorf("where: %w", err)
		}
		c, err := compileCond(w, t.cols[ci].kind)
		if err != nil {
			return nil, nil, fmt.Errorf("where: %w", err)
		}
		tests = append(tests, test{ci, c})
	}
	var out []aRow
	for _, r := range t.rows {
		ok := true
		for _, ts := range tests {
			if !ts.c.match(r.cells[ts.col]) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, r)
		}
	}
	var notes []string
	if len(out) == 0 && len(tests) > 0 {
		for _, ts := range tests {
			notes = append(notes, fmt.Sprintf("no row matched; %q has: %s", t.cols[ts.col].name, t.sampleValues(ts.col, 12)))
		}
	}
	return out, notes, nil
}

func (t *aTable) sampleValues(col, n int) string {
	seen := map[string]bool{}
	var vals []string
	for _, r := range t.rows {
		s := aText(r.cells[col])
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		vals = append(vals, s)
		if len(vals) == n {
			vals = append(vals, "...")
			break
		}
	}
	return strings.Join(vals, ", ")
}

func (t *aTable) keyIndexes(names []string) ([]int, error) {
	idx := make([]int, len(names))
	for i, n := range names {
		ci, err := t.colIndex(n)
		if err != nil {
			return nil, fmt.Errorf("group_by: %w", err)
		}
		idx[i] = ci
	}
	return idx, nil
}

func (t *aTable) buildMetrics(ms []aMetric, taken map[string]bool) ([]mspec, error) {
	if len(ms) == 0 {
		ms = []aMetric{{Fn: "count"}}
	}
	perCol := map[string]int{}
	for _, m := range ms {
		perCol[normText(m.Col)]++
	}
	out := make([]mspec, 0, len(ms))
	for _, m := range ms {
		fn, ok := fnAlias[strings.ToLower(strings.TrimSpace(m.Fn))]
		if !ok {
			return nil, fmt.Errorf("metric fn %q is unknown (use sum, avg, min, max, median, count, count_distinct, first, last, weighted_avg)", m.Fn)
		}
		s := mspec{col: -1, weight: -1, fn: fn, kind: ckNum}
		if strings.TrimSpace(m.Col) != "" {
			ci, err := t.colIndex(m.Col)
			if err != nil {
				return nil, fmt.Errorf("metrics: %w", err)
			}
			s.col = ci
		} else if fn != "count" {
			return nil, fmt.Errorf("metric %q needs a col", fn)
		}
		if s.col >= 0 {
			c := t.cols[s.col]
			switch fn {
			case "sum", "avg", "median", "weighted_avg":
				if c.kind != ckNum {
					return nil, fmt.Errorf("cannot %s column %q: it holds %s, not numbers", fn, c.name, kindName(c.kind))
				}
			case "min", "max":
				if c.kind == ckText {
					return nil, fmt.Errorf("cannot take the %s of text column %q", fn, c.name)
				}
			}
			if fn != "count" && fn != "count_distinct" {
				s.unit = c.unit
			}
			if fn == "min" || fn == "max" || fn == "first" || fn == "last" {
				s.kind = c.kind
			}
		}
		if fn == "weighted_avg" {
			if strings.TrimSpace(m.Weight) == "" {
				return nil, errors.New("weighted_avg needs weight (the weight column)")
			}
			wi, err := t.colIndex(m.Weight)
			if err != nil {
				return nil, fmt.Errorf("metrics weight: %w", err)
			}
			if t.cols[wi].kind != ckNum {
				return nil, fmt.Errorf("weight column %q is not numeric", t.cols[wi].name)
			}
			s.weight = wi
		}
		name := strings.TrimSpace(m.As)
		if name == "" {
			switch {
			case s.col < 0:
				name = "rows"
			case perCol[normText(m.Col)] == 1 && fn != "count" && fn != "count_distinct":
				name = t.cols[s.col].name
			default:
				name = t.cols[s.col].name + "_" + fn
			}
		}
		for base, n := name, 2; taken[name]; n++ {
			if n == 2 {
				name = base + "_" + fn
			} else {
				name = fmt.Sprintf("%s_%d", base, n)
			}
		}
		taken[name] = true
		s.name = name
		out = append(out, s)
	}
	return out, nil
}

func kindName(k cellKind) string {
	switch k {
	case ckNum:
		return "numbers"
	case ckDate:
		return "dates"
	}
	return "text"
}

// calc evaluates one metric over rows. A group with nothing to compute gives a
// blank cell, never a zero.
func calc(rows []aRow, m mspec) aCell {
	if m.fn == "count" && m.col < 0 {
		return numCell(float64(len(rows)), false)
	}
	switch m.fn {
	case "count":
		n := 0
		for _, r := range rows {
			if r.cells[m.col].kind != ckBlank {
				n++
			}
		}
		return numCell(float64(n), false)
	case "count_distinct":
		seen := map[string]bool{}
		for _, r := range rows {
			if c := r.cells[m.col]; c.kind != ckBlank {
				seen[aText(c)] = true
			}
		}
		return numCell(float64(len(seen)), false)
	case "first", "last":
		var pick aCell
		for _, r := range rows {
			if c := r.cells[m.col]; c.kind != ckBlank {
				pick = c
				if m.fn == "first" {
					break
				}
			}
		}
		return pick
	case "weighted_avg":
		var sw, swv float64
		for _, r := range rows {
			v, w := r.cells[m.col], r.cells[m.weight]
			if v.kind == ckNum && w.kind == ckNum {
				swv += v.n * w.n
				sw += w.n
			}
		}
		if sw == 0 {
			return aCell{}
		}
		return numCell(swv/sw, m.unit == "%")
	}
	if m.kind == ckDate && (m.fn == "min" || m.fn == "max") {
		var pick aCell
		for _, r := range rows {
			c := r.cells[m.col]
			if c.kind != ckDate {
				continue
			}
			if pick.kind == ckBlank || (m.fn == "min" && c.t.Before(pick.t)) || (m.fn == "max" && c.t.After(pick.t)) {
				pick = c
			}
		}
		return pick
	}
	vals := make([]float64, 0, len(rows))
	for _, r := range rows {
		if c := r.cells[m.col]; c.kind == ckNum {
			vals = append(vals, c.n)
		}
	}
	if len(vals) == 0 {
		return aCell{}
	}
	pct := m.unit == "%"
	switch m.fn {
	case "sum", "avg":
		s := 0.0
		for _, v := range vals {
			s += v
		}
		if m.fn == "avg" {
			s /= float64(len(vals))
		}
		return numCell(s, pct)
	case "min", "max":
		best := vals[0]
		for _, v := range vals[1:] {
			if (m.fn == "min" && v < best) || (m.fn == "max" && v > best) {
				best = v
			}
		}
		return numCell(best, pct)
	case "median":
		sort.Float64s(vals)
		mid := len(vals) / 2
		if len(vals)%2 == 1 {
			return numCell(vals[mid], pct)
		}
		return numCell((vals[mid-1]+vals[mid])/2, pct)
	}
	return aCell{}
}

type grp struct {
	key  []aCell
	rows []aRow
}

// groupRows splits rows by the key columns, in order of first appearance.
func groupRows(rows []aRow, idx []int) []*grp {
	if len(idx) == 0 {
		return []*grp{{rows: rows}}
	}
	var order []*grp
	byKey := map[string]*grp{}
	for _, r := range rows {
		parts := make([]string, len(idx))
		for i, c := range idx {
			parts[i] = aText(r.cells[c])
		}
		k := strings.Join(parts, "\x1f")
		g := byKey[k]
		if g == nil {
			key := make([]aCell, len(idx))
			for i, c := range idx {
				key[i] = r.cells[c]
				if key[i].kind == ckBlank {
					key[i] = aCell{kind: ckText, s: "(blank)"}
				}
			}
			g = &grp{key: key}
			byKey[k] = g
			order = append(order, g)
		}
		g.rows = append(g.rows, r)
	}
	return order
}

func (t *aTable) keyCols(idx []int) []rcol {
	cols := make([]rcol, len(idx))
	for i, c := range idx {
		cols[i] = rcol{name: t.cols[c].name, unit: t.cols[c].unit, kind: t.cols[c].kind}
	}
	return cols
}

func nameSet(cols []rcol) map[string]bool {
	m := map[string]bool{}
	for _, c := range cols {
		m[c.name] = true
	}
	return m
}

func (t *aTable) aggregate(rows []aRow, q aQuery) (*rtab, error) {
	idx, err := t.keyIndexes(q.GroupBy)
	if err != nil {
		return nil, err
	}
	rt := &rtab{cols: t.keyCols(idx)}
	ms, err := t.buildMetrics(q.Metrics, nameSet(rt.cols))
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		rt.cols = append(rt.cols, rcol{name: m.name, unit: m.unit, kind: m.kind})
	}
	for _, g := range groupRows(rows, idx) {
		line := append([]aCell{}, g.key...)
		for _, m := range ms {
			line = append(line, calc(g.rows, m))
		}
		rt.rows = append(rt.rows, line)
	}
	return rt, nil
}

// compare puts the current period next to the previous one per group.
func (t *aTable) compare(rows []aRow, q aQuery) (*rtab, []aRow, []string, error) {
	if strings.TrimSpace(q.PeriodCol) == "" || q.Current == nil || q.Previous == nil {
		return nil, nil, nil, errors.New("compare needs period_col, current and previous")
	}
	pc, err := t.colIndex(q.PeriodCol)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("period_col: %w", err)
	}
	curC, err := compileCond(aCond{Col: q.PeriodCol, Op: "=", Value: q.Current}, t.cols[pc].kind)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("current: %w", err)
	}
	prevC, err := compileCond(aCond{Col: q.PeriodCol, Op: "=", Value: q.Previous}, t.cols[pc].kind)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("previous: %w", err)
	}
	var cur, prev []aRow
	for _, r := range rows {
		switch c := r.cells[pc]; {
		case curC.match(c):
			cur = append(cur, r)
		case prevC.match(c):
			prev = append(prev, r)
		}
	}
	var notes []string
	if len(cur) == 0 || len(prev) == 0 {
		which := q.Current
		if len(cur) != 0 {
			which = q.Previous
		}
		return nil, nil, nil, fmt.Errorf("no row has period %v in %q; it has: %s", which, t.cols[pc].name, t.sampleValues(pc, 12))
	}
	idx, err := t.keyIndexes(q.GroupBy)
	if err != nil {
		return nil, nil, nil, err
	}
	rt := &rtab{cols: t.keyCols(idx)}
	ms, err := t.buildMetrics(q.Metrics, nameSet(rt.cols))
	if err != nil {
		return nil, nil, nil, err
	}
	for _, m := range ms {
		chg := m.unit
		if m.unit == "%" {
			chg = "pp"
		}
		rt.cols = append(rt.cols,
			rcol{name: m.name, unit: m.unit, kind: ckNum},
			rcol{name: m.name + "_prev", unit: m.unit, kind: ckNum},
			rcol{name: m.name + "_change", unit: chg, kind: ckNum},
			rcol{name: m.name + "_change_pct", unit: "%", kind: ckNum})
	}
	prevBy := map[string]*grp{}
	keyOf := func(g *grp) string {
		parts := make([]string, len(g.key))
		for i, c := range g.key {
			parts[i] = aText(c)
		}
		return strings.Join(parts, "\x1f")
	}
	pg := groupRows(prev, idx)
	for _, g := range pg {
		prevBy[keyOf(g)] = g
	}
	seen := map[string]bool{}
	var onlyCur, onlyPrev []string
	emit := func(key []aCell, cg, pgp *grp) {
		line := append([]aCell{}, key...)
		for _, m := range ms {
			var vc, vp aCell
			if cg != nil {
				vc = calc(cg.rows, m)
			}
			if pgp != nil {
				vp = calc(pgp.rows, m)
			}
			line = append(line, vc, vp)
			if vc.kind == ckNum && vp.kind == ckNum {
				d := vc.n - vp.n
				line = append(line, numCell(d, false))
				if vp.n != 0 {
					line = append(line, numCell(d/math.Abs(vp.n)*100, false))
				} else {
					line = append(line, aCell{})
				}
			} else {
				line = append(line, aCell{}, aCell{})
			}
		}
		rt.rows = append(rt.rows, line)
	}
	for _, g := range groupRows(cur, idx) {
		k := keyOf(g)
		seen[k] = true
		p := prevBy[k]
		if p == nil && len(idx) > 0 {
			onlyCur = append(onlyCur, k)
		}
		emit(g.key, g, p)
	}
	for _, g := range pg {
		if k := keyOf(g); !seen[k] {
			onlyPrev = append(onlyPrev, k)
			emit(g.key, nil, g)
		}
	}
	if len(onlyCur) > 0 {
		notes = append(notes, "only in the current period (no comparison): "+strings.Join(limitStrings(onlyCur, 10), ", "))
	}
	if len(onlyPrev) > 0 {
		notes = append(notes, "only in the previous period: "+strings.Join(limitStrings(onlyPrev, 10), ", "))
	}
	return rt, append(append([]aRow{}, cur...), prev...), notes, nil
}

// pivot spreads one metric over the values of pivot_col.
func (t *aTable) pivot(rows []aRow, q aQuery) (*rtab, error) {
	if strings.TrimSpace(q.PivotCol) == "" {
		return nil, errors.New("pivot needs pivot_col")
	}
	if len(q.Metrics) != 1 {
		return nil, errors.New("pivot takes exactly one metric")
	}
	pc, err := t.colIndex(q.PivotCol)
	if err != nil {
		return nil, fmt.Errorf("pivot_col: %w", err)
	}
	idx, err := t.keyIndexes(q.GroupBy)
	if err != nil {
		return nil, err
	}
	rt := &rtab{cols: t.keyCols(idx)}
	ms, err := t.buildMetrics(q.Metrics, nameSet(rt.cols))
	if err != nil {
		return nil, err
	}
	m := ms[0]
	var heads []aCell
	seen := map[string]bool{}
	for _, r := range rows {
		c := r.cells[pc]
		if c.kind == ckBlank || seen[aText(c)] {
			continue
		}
		seen[aText(c)] = true
		heads = append(heads, c)
	}
	if t.cols[pc].kind == ckDate {
		sort.SliceStable(heads, func(i, j int) bool { return heads[i].t.Before(heads[j].t) })
	}
	if len(heads) > 60 {
		return nil, fmt.Errorf("pivot_col %q has %d distinct values; filter first", t.cols[pc].name, len(heads))
	}
	for _, h := range heads {
		rt.cols = append(rt.cols, rcol{name: aText(h), unit: m.unit, kind: m.kind})
	}
	for _, g := range groupRows(rows, idx) {
		line := append([]aCell{}, g.key...)
		for _, h := range heads {
			var sub []aRow
			for _, r := range g.rows {
				if aText(r.cells[pc]) == aText(h) {
					sub = append(sub, r)
				}
			}
			if len(sub) == 0 {
				line = append(line, aCell{})
			} else {
				line = append(line, calc(sub, m))
			}
		}
		rt.rows = append(rt.rows, line)
	}
	return rt, nil
}

// listRows returns matching source rows with the chosen columns.
func (t *aTable) listRows(rows []aRow, q aQuery) (*rtab, error) {
	var idx []int
	if len(q.Select) > 0 {
		for _, n := range q.Select {
			ci, err := t.colIndex(n)
			if err != nil {
				return nil, fmt.Errorf("select: %w", err)
			}
			idx = append(idx, ci)
		}
	} else {
		for ci, c := range t.cols {
			if c.nonBlank > 0 && len(idx) < 15 {
				idx = append(idx, ci)
			}
		}
	}
	rt := &rtab{cols: t.keyCols(idx)}
	for _, r := range rows {
		line := make([]aCell, len(idx))
		for i, c := range idx {
			line[i] = r.cells[c]
		}
		rt.rows = append(rt.rows, line)
	}
	return rt, nil
}

func (rt *rtab) colIndex(want string) (int, error) {
	names := make([]string, len(rt.cols))
	for i, c := range rt.cols {
		names[i] = c.name
	}
	i, amb, ok := findName(names, want)
	switch {
	case ok:
		return i, nil
	case len(amb) > 0:
		return -1, fmt.Errorf("result column %q is ambiguous: %s", want, nameList(amb))
	}
	return -1, fmt.Errorf("no result column %q; result columns: %s", want, nameList(names))
}

func lessCell(a, b aCell) bool {
	switch {
	case a.kind == ckNum && b.kind == ckNum:
		return a.n < b.n
	case a.kind == ckDate && b.kind == ckDate:
		return a.t.Before(b.t)
	}
	return strings.Compare(aText(a), aText(b)) < 0
}

// decimalsFor picks the rounding for a result column: the caller's choice, else
// 2 places, else 4 when every value of a unit-less column is a fraction below 1.
func (rt *rtab) decimalsFor(ci int, set *int) int {
	if set != nil {
		return min(max(*set, 0), 8)
	}
	if rt.cols[ci].unit != "" {
		return 2
	}
	frac := false
	for _, r := range rt.rows {
		c := r[ci]
		if c.kind != ckNum {
			continue
		}
		if math.Abs(c.n) >= 1 {
			return 2
		}
		if c.n != 0 {
			frac = true
		}
	}
	if frac {
		return 4
	}
	return 2
}

// finish applies having, order_by, rank and limit, in that order.
func (rt *rtab) finish(q aQuery, def int) (matched int, truncated bool, tie string, err error) {
	for _, h := range q.Having {
		ci, e := rt.colIndex(h.Col)
		if e != nil {
			return 0, false, "", fmt.Errorf("having: %w", e)
		}
		c, e := compileCond(h, rt.cols[ci].kind)
		if e != nil {
			return 0, false, "", fmt.Errorf("having: %w", e)
		}
		kept := rt.rows[:0:0]
		for _, r := range rt.rows {
			if c.match(r[ci]) {
				kept = append(kept, r)
			}
		}
		rt.rows = kept
	}
	oi := -1
	if strings.TrimSpace(q.OrderBy) != "" {
		if oi, err = rt.colIndex(q.OrderBy); err != nil {
			return 0, false, "", fmt.Errorf("order_by: %w", err)
		}
		sort.SliceStable(rt.rows, func(i, j int) bool {
			a, b := rt.rows[i][oi], rt.rows[j][oi]
			switch {
			case a.kind == ckBlank:
				return false
			case b.kind == ckBlank:
				return true
			case q.Desc:
				return lessCell(b, a)
			}
			return lessCell(a, b)
		})
	}
	if q.Rank {
		if oi < 0 {
			return 0, false, "", errors.New("rank needs order_by")
		}
		dec := rt.decimalsFor(oi, q.Decimals)
		rt.cols = append([]rcol{{name: "rank", kind: ckNum}}, rt.cols...)
		rank, prev := 0, aCell{}
		for i, r := range rt.rows {
			cur := r[oi]
			same := i > 0 && cur.kind == prev.kind && roundTo(cur.n, dec) == roundTo(prev.n, dec) && cur.s == prev.s && cur.t.Equal(prev.t)
			if !same {
				rank = i + 1
			}
			prev = cur
			rt.rows[i] = append([]aCell{numCell(float64(rank), false)}, r...)
		}
	}
	matched = len(rt.rows)
	limit := q.Limit
	if limit <= 0 {
		limit = def
	}
	limit = min(limit, maxResultRows)
	if matched > limit {
		if oi >= 0 && !q.Rank { // oi is not shifted by the rank column
			tie = rt.tieAtCut(oi, limit, q.Decimals)
		} else if oi >= 0 {
			tie = rt.tieAtCut(oi+1, limit, q.Decimals)
		}
		rt.rows, truncated = rt.rows[:limit], true
	}
	return matched, truncated, tie, nil
}

// tieAtCut reports when the first row cut off by limit has the same sort value
// as the last row kept, so a top/bottom N list does not hide a tie.
func (rt *rtab) tieAtCut(oi, limit int, set *int) string {
	a, b := rt.rows[limit-1][oi], rt.rows[limit][oi]
	if a.kind != b.kind || a.kind == ckBlank {
		return ""
	}
	dec := rt.decimalsFor(oi, set)
	if (a.kind == ckNum && roundTo(a.n, dec) == roundTo(b.n, dec)) || (a.kind != ckNum && aText(a) == aText(b)) {
		return fmt.Sprintf("tie at the cut: the next row (after the %d shown) has the same %s value as the last one shown", limit, rt.cols[oi].name)
	}
	return ""
}

// render turns the table into JSON-ready rows: numbers rounded, text as is.
func (rt *rtab) render(set *int) [][]any {
	decs := make([]int, len(rt.cols))
	for ci := range rt.cols {
		decs[ci] = rt.decimalsFor(ci, set)
	}
	out := make([][]any, len(rt.rows))
	for i, r := range rt.rows {
		line := make([]any, len(r))
		for ci, c := range r {
			switch c.kind {
			case ckNum:
				line[ci] = number(roundTo(c.n, decs[ci]))
			case ckText, ckDate:
				line[ci] = c.s
			}
		}
		out[i] = line
	}
	return out
}

func (rt *rtab) units() map[string]string {
	var m map[string]string
	for _, c := range rt.cols {
		if c.unit != "" {
			if m == nil {
				m = map[string]string{}
			}
			m[c.name] = c.unit
		}
	}
	return m
}

// metricNotes tells the model what the calculation had to skip or convert in
// the rows it used, so a blank cell is never silently counted as zero.
func (t *aTable) metricNotes(rows []aRow, ms []int) []string {
	var notes []string
	done := map[int]bool{}
	for _, ci := range ms {
		if ci < 0 || done[ci] || t.cols[ci].kind != ckNum {
			continue
		}
		done[ci] = true
		var blanks, texts, bads []string
		nb, nt, nbad := 0, 0, 0
		for _, r := range rows {
			c := r.cells[ci]
			switch {
			case c.flag&flBad != 0:
				nbad++
				bads = append(bads, fmt.Sprintf("%s (%s)", t.cellRef(r, ci), t.label(r)))
			case c.kind == ckBlank:
				nb++
				blanks = append(blanks, fmt.Sprintf("%s (%s)", t.cellRef(r, ci), t.label(r)))
			case c.flag&flFromText != 0:
				nt++
				texts = append(texts, fmt.Sprintf("%s (%s)", t.cellRef(r, ci), t.label(r)))
			}
		}
		name := t.cols[ci].name
		if nb > 0 {
			notes = append(notes, fmt.Sprintf("%s: %d blank cell(s) among the %d rows used were left out of the calculation: %s",
				name, nb, len(rows), strings.Join(limitStrings(blanks, maxIssueLabels), ", ")))
		}
		if nbad > 0 {
			notes = append(notes, fmt.Sprintf("%s: %d cell(s) hold text that is not a number and were left out: %s",
				name, nbad, strings.Join(limitStrings(bads, maxIssueLabels), ", ")))
		}
		if nt > 0 {
			notes = append(notes, fmt.Sprintf("%s: %d number(s) stored as text were read as numbers: %s",
				name, nt, strings.Join(limitStrings(texts, maxIssueLabels), ", ")))
		}
	}
	return notes
}
