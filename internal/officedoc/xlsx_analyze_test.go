package officedoc

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/xuri/excelize/v2"
)

// writeBulletinBook writes the kind of workbook a network department keeps: a
// merged title, a note line, the header on row 3, months as real dates, a city
// name merged over its rows, percentages stored as 0.995 and as the text
// "93.8%", one blank cell, a total row with formulas and a footnote.
func writeBulletinBook(t *testing.T, path string) {
	t.Helper()
	f := excelize.NewFile()
	sh := "月度指标"
	f.SetSheetName("Sheet1", sh)
	set := func(cell string, v any) {
		if err := f.SetCellValue(sh, cell, v); err != nil {
			t.Fatal(err)
		}
	}
	set("A1", "2026年7-9月全省各地市网络指标汇总")
	if err := f.MergeCell(sh, "A1", "H1"); err != nil {
		t.Fatal(err)
	}
	set("A2", "填报单位：网络部    单位说明：覆盖率为百分比")
	for i, h := range []string{"地市", "月份", "5G覆盖率", "4G覆盖率", "投诉量(件)", "重大故障(起)", "新增站点(个)", "备注"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 3)
		set(cell, h)
	}
	pct, monthFmt, thousands := "0.0%", "yyyy-mm", "#,##0"
	pctStyle, _ := f.NewStyle(&excelize.Style{CustomNumFmt: &pct})
	monthStyle, _ := f.NewStyle(&excelize.Style{CustomNumFmt: &monthFmt})
	numStyle, _ := f.NewStyle(&excelize.Style{CustomNumFmt: &thousands})

	type rec struct {
		city              string
		g5                any
		g4                float64
		comp, fault, site int
		note              string
	}
	data := [][]rec{
		{{"济南", 0.995, .991, 392, 0, 18, ""}, {"济南", 0.995, .989, 497, 2, 27, ""}, {"济南", 0.995, .991, 529, 2, 20, ""}},
		{{"青岛", 0.995, .994, 455, 1, 33, ""}, {"青岛", 0.995, .988, 392, 0, 18, ""}, {"青岛", 0.995, .990, 506, 0, 12, "含世园会保障"}},
		{{"淄博", 0.948, .990, 181, 3, 9, ""}, {"淄博", 0.953, .986, 293, 1, 20, ""}, {"淄博", 0.9546, .992, 267, 2, 18, ""}},
		{{"德州", nil, .985, 250, 0, 7, ""}, {"德州", "93.8%", .986, 280, 1, 9, ""}, {"德州", 0.9459, .987, 300, 1, 10, ""}},
	}
	row := 4
	for _, city := range data {
		for m, r := range city {
			a := func(col string) string { return fmt.Sprintf("%s%d", col, row) }
			if m == 0 {
				set(a("A"), r.city)
			} else if r.city != "济南" {
				set(a("A"), r.city)
			}
			set(a("B"), time.Date(2026, time.Month(7+m), 1, 0, 0, 0, 0, time.UTC))
			_ = f.SetCellStyle(sh, a("B"), a("B"), monthStyle)
			if r.g5 != nil {
				set(a("C"), r.g5)
				if _, text := r.g5.(string); !text {
					_ = f.SetCellStyle(sh, a("C"), a("C"), pctStyle)
				}
			}
			set(a("D"), r.g4)
			_ = f.SetCellStyle(sh, a("D"), a("D"), pctStyle)
			set(a("E"), r.comp)
			_ = f.SetCellStyle(sh, a("E"), a("E"), numStyle)
			set(a("F"), r.fault)
			set(a("G"), r.site)
			if r.note != "" {
				set(a("H"), r.note)
			}
			row++
		}
	}
	if err := f.MergeCell(sh, "A4", "A6"); err != nil { // 济南 merged over its three months
		t.Fatal(err)
	}
	set("A16", "全省合计/平均")
	_ = f.SetCellFormula(sh, "C16", "AVERAGE(C4:C15)")
	_ = f.SetCellFormula(sh, "E16", "SUM(E4:E15)")
	set("A18", "注：数据来源于网管系统，仅供内部使用")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
}

func analyze(t *testing.T, ts []tool.BaseTool, args map[string]any) analyzeXlsxOutput {
	t.Helper()
	raw, err := run(t, ToolAnalyzeXlsx, ts, args)
	if err != nil {
		t.Fatalf("analyze_xlsx: %v", err)
	}
	var out analyzeXlsxOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("bad reply: %v\n%s", err, raw)
	}
	return out
}

func bulletinTools(t *testing.T) []tool.BaseTool {
	t.Helper()
	root, ts := newXlsxTools(t)
	writeBulletinBook(t, root+"/月度指标.xlsx")
	return ts
}

func resultByName(t *testing.T, out analyzeXlsxOutput, name string) aResult {
	t.Helper()
	for _, r := range out.Results {
		if r.Name == name {
			if r.Error != "" {
				t.Fatalf("query %q failed: %s", name, r.Error)
			}
			return r
		}
	}
	t.Fatalf("no result %q in %+v", name, out.Results)
	return aResult{}
}

func num(t *testing.T, v any) float64 {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("want a number, got %#v", v)
	}
	return f
}

func wantNum(t *testing.T, what string, got any, want float64) {
	t.Helper()
	f, ok := got.(float64)
	if !ok || math.Abs(f-want) > 1e-6 {
		t.Errorf("%s = %#v, want %v", what, got, want)
	}
}

func TestAnalyzeProfileFindsHeaderAndDataProblems(t *testing.T) {
	ts := bulletinTools(t)
	out := analyze(t, ts, map[string]any{"relative_path": "月度指标.xlsx"})
	if out.HeaderRow != 3 || out.DataRows != 12 {
		t.Fatalf("header_row=%d data_rows=%d, want 3 and 12 (title, note, total and footnote rows are not data)", out.HeaderRow, out.DataRows)
	}
	joined := strings.Join(out.Notes, "\n")
	if !strings.Contains(joined, "row 16") || !strings.Contains(joined, "total row") {
		t.Errorf("the total row must be reported as left out: %v", out.Notes)
	}
	if !strings.Contains(joined, "row 18") {
		t.Errorf("the footnote row must be reported: %v", out.Notes)
	}
	cols := map[string]aColInfo{}
	for _, c := range out.Results[0].Profile {
		cols[c.Name] = c
	}
	g5 := cols["5G覆盖率"]
	if g5.Type != "percent" || g5.Unit != "%" || g5.Blank != 1 {
		t.Errorf("5G column = %+v, want percent with 1 blank", g5)
	}
	wantNum(t, "5G max", g5.Max, 99.5)
	issues := strings.Join(g5.Issues, " | ")
	if !strings.Contains(issues, "C13 (德州 2026-07)") || !strings.Contains(issues, "stored as text") || !strings.Contains(issues, "C14") {
		t.Errorf("issues should name the blank and the text cell with their addresses: %v", g5.Issues)
	}
	if c := cols["月份"]; c.Type != "date" || c.Range != "2026-07 ~ 2026-09" {
		t.Errorf("month column = %+v", c)
	}
	if c := cols["地市"]; c.Type != "text" || c.Distinct != 4 || len(c.Values) != 4 {
		t.Errorf("city column = %+v (the merged 济南 cell must fill its three rows)", c)
	}
	if c := cols["投诉量(件)"]; c.Type != "number" {
		t.Errorf("complaints column = %+v", c)
	}
}

func TestAnalyzeAggregateRankingAndFilters(t *testing.T) {
	ts := bulletinTools(t)
	sep := []map[string]any{{"col": "月份", "op": "=", "value": "2026-09"}}
	out := analyze(t, ts, map[string]any{"relative_path": "月度指标.xlsx", "queries": []map[string]any{
		{"name": "total", "where": sep, "metrics": []map[string]any{
			{"col": "5G覆盖率", "fn": "avg", "as": "g5"},
			{"col": "投诉量", "fn": "sum", "as": "complaints"},
			{"col": "重大故障", "fn": "sum", "as": "faults"},
			{"col": "新增站点", "fn": "sum", "as": "sites"},
			{"col": "5G覆盖率", "fn": "weighted_avg", "weight": "新增站点", "as": "g5w"},
		}},
		{"name": "rank", "where": sep, "group_by": []string{"地市"}, "metrics": []map[string]any{{"col": "5G覆盖率", "fn": "avg"}},
			"order_by": "5G覆盖率", "desc": true, "rank": true},
		{"name": "top1", "where": sep, "group_by": []string{"地市"}, "metrics": []map[string]any{{"col": "5G覆盖率", "fn": "avg"}},
			"order_by": "5G覆盖率", "desc": true, "limit": 1},
		{"name": "bottom2", "where": sep, "group_by": []string{"地市"}, "metrics": []map[string]any{{"col": "5G覆盖率", "fn": "avg"}},
			"order_by": "5G覆盖率", "limit": 2},
		{"name": "high", "where": sep, "group_by": []string{"地市"}, "metrics": []map[string]any{{"col": "投诉量", "fn": "sum"}},
			"having": []map[string]any{{"col": "投诉量", "op": ">", "value": 450}}, "order_by": "投诉量", "desc": true},
		{"name": "below", "where": sep, "group_by": []string{"地市"}, "metrics": []map[string]any{{"col": "5G覆盖率", "fn": "avg"}},
			"having": []map[string]any{{"col": "5G覆盖率", "op": "<", "value": 96}}},
	}})
	tot := resultByName(t, out, "total")
	if len(tot.Rows) != 1 || tot.MatchedRows != 4 {
		t.Fatalf("total = %+v", tot)
	}
	want := []float64{97.26, 1602, 5, 60, 97.47}
	for i, w := range want {
		wantNum(t, tot.Columns[i], tot.Rows[0][i], w)
	}
	if tot.Units["g5"] != "%" {
		t.Errorf("a percent column must report its unit: %v", tot.Units)
	}

	rank := resultByName(t, out, "rank")
	if rank.Columns[0] != "rank" || len(rank.Rows) != 4 {
		t.Fatalf("rank = %+v", rank)
	}
	wantRank := []struct {
		city string
		rank float64
		g5   float64
	}{{"济南", 1, 99.5}, {"青岛", 1, 99.5}, {"淄博", 3, 95.46}, {"德州", 4, 94.59}}
	for i, w := range wantRank {
		r := rank.Rows[i]
		if r[1] != w.city || num(t, r[0]) != w.rank {
			t.Errorf("rank row %d = %v, want %v (ties share a rank)", i, r, w)
		}
		wantNum(t, "g5 of "+w.city, r[2], w.g5)
	}

	top1 := resultByName(t, out, "top1")
	if len(top1.Rows) != 1 || !strings.Contains(strings.Join(top1.Notes, " "), "tie at the cut") {
		t.Errorf("top 1 of a tie must say the next row is tied: %+v", top1)
	}
	if b2 := resultByName(t, out, "bottom2"); len(b2.Notes) != 0 {
		t.Errorf("a limit the caller asked for needs no truncation note: %v", b2.Notes)
	}
	b2 := resultByName(t, out, "bottom2")
	if len(b2.Rows) != 2 || b2.Rows[0][0] != "德州" || b2.Rows[1][0] != "淄博" || b2.RowCount != 4 || !b2.Truncated {
		t.Errorf("bottom2 = %+v", b2)
	}
	high := resultByName(t, out, "high")
	if len(high.Rows) != 2 || high.Rows[0][0] != "济南" || high.Rows[1][0] != "青岛" {
		t.Errorf("complaints above 450 = %v", high.Rows)
	}
	below := resultByName(t, out, "below")
	if len(below.Rows) != 2 || below.Rows[0][0] != "淄博" || below.Rows[1][0] != "德州" {
		t.Errorf("below the 96 target = %v", below.Rows)
	}
}

func TestAnalyzeCompareAndPivot(t *testing.T) {
	ts := bulletinTools(t)
	g5 := []map[string]any{{"col": "5G覆盖率", "fn": "avg"}}
	out := analyze(t, ts, map[string]any{"relative_path": "月度指标.xlsx", "queries": []map[string]any{
		{"name": "mom", "op": "compare", "period_col": "月份", "current": "2026-09", "previous": "2026-08",
			"group_by": []string{"地市"}, "metrics": g5, "order_by": "5G覆盖率_change", "desc": true},
		{"name": "gap", "op": "compare", "period_col": "月份", "current": "2026年8月", "previous": "7月",
			"group_by": []string{"地市"}, "metrics": g5},
		{"name": "overall", "op": "compare", "period_col": "月份", "current": "2026-09", "previous": "2026-08",
			"metrics": []map[string]any{{"col": "投诉量", "fn": "sum"}}},
		{"name": "trend", "op": "pivot", "pivot_col": "月份", "group_by": []string{"地市"}, "metrics": []map[string]any{{"col": "投诉量", "fn": "sum"}}},
	}})
	mom := resultByName(t, out, "mom")
	wantCols := "地市,5G覆盖率,5G覆盖率_prev,5G覆盖率_change,5G覆盖率_change_pct"
	if strings.Join(mom.Columns, ",") != wantCols {
		t.Fatalf("compare columns = %v", mom.Columns)
	}
	if mom.Units["5G覆盖率_change"] != "pp" {
		t.Errorf("a change in a percent column is in percentage points: %v", mom.Units)
	}
	first := mom.Rows[0] // 德州: 94.59 now, the text "93.8%" before
	if first[0] != "德州" {
		t.Fatalf("largest improvement should be 德州, got %v", mom.Rows)
	}
	wantNum(t, "德州 now", first[1], 94.59)
	wantNum(t, "德州 before (text number)", first[2], 93.8)
	wantNum(t, "德州 change", first[3], 0.79)
	wantNum(t, "淄博 change %", mom.Rows[1][4], roundTo(0.16/95.3*100, 2)) // a percent change keeps 2 decimals
	last := mom.Rows[3]
	if last[0] != "济南" && last[0] != "青岛" {
		t.Errorf("flat cities sort last: %v", mom.Rows)
	}
	wantNum(t, "flat change", last[3], 0)

	gap := resultByName(t, out, "gap")
	var dz []any
	for _, r := range gap.Rows {
		if r[0] == "德州" {
			dz = r
		}
	}
	if dz == nil || dz[2] != nil || dz[3] != nil {
		t.Errorf("the blank July cell must give a blank comparison, not a zero: %v", gap.Rows)
	}
	if !strings.Contains(strings.Join(gap.Notes, "\n"), "C13 (德州 2026-07)") {
		t.Errorf("notes should say which blank cell was left out: %v", gap.Notes)
	}

	overall := resultByName(t, out, "overall")
	wantNum(t, "complaints now", overall.Rows[0][0+0], 1602)
	wantNum(t, "complaints before", overall.Rows[0][1], 497+392+293+280)
	wantNum(t, "change pct", overall.Rows[0][3], roundTo((1602.0-1462)/1462*100, 2))

	trend := resultByName(t, out, "trend")
	if strings.Join(trend.Columns, ",") != "地市,2026-07,2026-08,2026-09" || len(trend.Rows) != 4 {
		t.Fatalf("pivot = %+v", trend)
	}
	wantNum(t, "济南 Sep", trend.Rows[0][3], 529)
}

func TestAnalyzeMonthSpellingsAndErrors(t *testing.T) {
	ts := bulletinTools(t)
	var queries []map[string]any
	for _, v := range []any{"2026-09", "2026/9", "2026年9月", "9月", "2026-09-15", "202609", 9} {
		queries = append(queries, map[string]any{"name": fmt.Sprint(v), "op": "rows", "select": []string{"地市"},
			"where": []map[string]any{{"col": "月份", "op": "=", "value": v}}})
	}
	queries = append(queries,
		map[string]any{"name": "range", "metrics": []map[string]any{{"fn": "count"}},
			"where": []map[string]any{{"col": "月份", "op": ">=", "value": "2026-08"}}},
		map[string]any{"name": "nocol", "where": []map[string]any{{"col": "没有这列", "op": "=", "value": 1}}},
		map[string]any{"name": "nomatch", "where": []map[string]any{{"col": "地市", "op": "=", "value": "北京"}}},
		map[string]any{"name": "text", "metrics": []map[string]any{{"col": "地市", "fn": "sum"}}},
		map[string]any{"name": "letter", "op": "rows", "select": []string{"C"}, "limit": 1},
	)
	out := analyze(t, ts, map[string]any{"relative_path": "月度指标.xlsx", "queries": queries})
	for _, v := range []string{"2026-09", "2026/9", "2026年9月", "9月"} {
		if r := resultByName(t, out, v); r.MatchedRows != 4 {
			t.Errorf("month %q matched %d rows, want 4", v, r.MatchedRows)
		}
	}
	if r := resultByName(t, out, "2026-09-15"); r.MatchedRows != 0 {
		t.Errorf("a full date that is not in the data must not match a month: %d", r.MatchedRows)
	}
	if r := resultByName(t, out, "202609"); r.MatchedRows != 4 {
		t.Errorf("yyyymm should match: %d", r.MatchedRows)
	}
	if r := resultByName(t, out, "9"); r.MatchedRows != 4 {
		t.Errorf("a bare 9 means September in a month column: %d", r.MatchedRows)
	}
	rng := resultByName(t, out, "range")
	wantNum(t, "rows from 2026-08", rng.Rows[0][0], 8)
	for name, want := range map[string]string{"nocol": "columns:", "text": "not numbers"} {
		var got string
		for _, r := range out.Results {
			if r.Name == name {
				got = r.Error
			}
		}
		if !strings.Contains(got, want) {
			t.Errorf("%s error = %q, want it to contain %q", name, got, want)
		}
	}
	for _, r := range out.Results {
		if r.Name == "nomatch" && !strings.Contains(strings.Join(r.Notes, " "), "济南") {
			t.Errorf("an empty match should list the values the column does have: %+v", r)
		}
	}
	if r := resultByName(t, out, "letter"); r.Columns[0] != "5G覆盖率" {
		t.Errorf("a column letter should resolve: %+v", r)
	}
}

func TestAnalyzeKeepsTotalRowsOnRequest(t *testing.T) {
	ts := bulletinTools(t)
	out := analyze(t, ts, map[string]any{"relative_path": "月度指标.xlsx", "keep_total_rows": true,
		"queries": []map[string]any{{"name": "n", "metrics": []map[string]any{{"fn": "count"}}}}})
	if got := num(t, resultByName(t, out, "n").Rows[0][0]); got != 13 {
		t.Errorf("rows with the total kept = %v, want 13", got)
	}
	// The formulas of the total row were calculated, not read as blanks.
	out = analyze(t, ts, map[string]any{"relative_path": "月度指标.xlsx", "keep_total_rows": true,
		"queries": []map[string]any{{"name": "t", "op": "rows", "where": []map[string]any{{"col": "地市", "op": "contains", "value": "合计"}},
			"select": []string{"地市", "投诉量"}}}})
	rows := resultByName(t, out, "t").Rows
	if len(rows) != 1 {
		t.Fatalf("total row = %v", rows)
	}
	wantNum(t, "SUM of complaints", rows[0][1], 4342)
}

func TestAnalyzeReadsWorkbookReadOnly(t *testing.T) {
	root, ts := newXlsxTools(t)
	writeBulletinBook(t, root+"/月度指标.xlsx")
	before, _ := os.ReadFile(root + "/月度指标.xlsx")
	analyze(t, ts, map[string]any{"relative_path": "月度指标.xlsx"})
	if after, _ := os.ReadFile(root + "/月度指标.xlsx"); string(after) != string(before) {
		t.Error("analyze_xlsx must not change the file")
	}
	if !ReadOnly(ToolAnalyzeXlsx) {
		t.Error("analyze_xlsx is read-only")
	}
	if _, err := run(t, ToolAnalyzeXlsx, ts, map[string]any{"relative_path": "nope.xlsx"}); err == nil {
		t.Error("a missing file must be an error")
	}
	if _, err := run(t, ToolAnalyzeXlsx, ts, map[string]any{"relative_path": "月度指标.xlsx", "sheet": "没有"}); err == nil ||
		!strings.Contains(err.Error(), "月度指标") {
		t.Errorf("an unknown sheet should list the sheets: %v", err)
	}
}

func TestLooseNumbersDatesAndCells(t *testing.T) {
	for in, want := range map[string]float64{"95.8%": 95.8, " 1,234 ": 1234, "￥12.5": 12.5, "－3": -3} {
		v, _, _, ok := looseNumber(in)
		if !ok || v != want {
			t.Errorf("looseNumber(%q) = %v, %v; want %v", in, v, ok, want)
		}
	}
	for _, in := range []string{"", "abc", "9月", "2026-09", "Inf", "NaN", "0x10"} {
		if _, _, _, ok := looseNumber(in); ok {
			t.Errorf("looseNumber(%q) should fail", in)
		}
	}
	if c := classifyCell("0.958", "95.8%"); c.kind != ckNum || !c.pct || c.n != 95.8 {
		t.Errorf("percent cell = %+v", c)
	}
	if c := classifyCell("95.8", `95.8%`); c.kind != ckNum || c.n != 95.8 {
		t.Errorf("literal percent format must not be multiplied again: %+v", c)
	}
	if c := classifyCell("007", "007"); c.kind != ckText || c.s != "007" {
		t.Errorf("an identifier with leading zeros is text: %+v", c)
	}
	if c := classifyCell("46266", "2026-09"); c.kind != ckDate || c.t.Month() != time.September {
		t.Errorf("date cell = %+v", c)
	}
	if c := classifyCell("1234", "1,234"); c.kind != ckNum || c.n != 1234 {
		t.Errorf("formatted number = %+v", c)
	}
	if c := classifyCell("1", "TRUE"); c.kind != ckText || c.s != "TRUE" {
		t.Errorf("boolean = %+v", c)
	}
	if p, ok := parseDatePat("2026年9月"); !ok || p != (datePat{2026, 9, 0}) {
		t.Errorf("2026年9月 = %+v %v", p, ok)
	}
	if _, ok := parseDatePat("13月"); ok {
		t.Error("month 13 is not a month")
	}
}
