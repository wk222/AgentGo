package officedoc

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

// The scenario skills (office-dashboard, office-financial-model) recommend
// particular formulas. read_xlsx and the results stored in the file come from
// excelize's calculator, so a recommended formula it gets wrong would make the
// model "fix" something that is right. This pins the recommended forms to
// numbers worked out by hand.
//
// One form is deliberately absent: excelize reads a range argument of NPV as its
// first cell only (NPV(0.1,B1:F1) gives 272.7 where Excel gives 1137.2), which is
// why office-financial-model tells the model to use a discount-factor row.

func readSheetRows(t *testing.T, ts []tool.BaseTool, rel, sheet string) [][]any {
	t.Helper()
	out, err := run(t, ToolReadXlsx, ts, map[string]any{"relative_path": rel, "sheet": sheet})
	if err != nil {
		t.Fatal(err)
	}
	var got readXlsxOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	return got.Rows
}

func wantNumber(t *testing.T, what string, got any, want float64) {
	t.Helper()
	n, ok := got.(float64)
	if !ok || math.Abs(n-want) > 1e-6 {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func TestDashboardSkillFormulasCompute(t *testing.T) {
	_, ts := newXlsxTools(t)
	_, err := run(t, ToolCreateXlsx, ts, map[string]any{
		"relative_path": "board.xlsx",
		"sheets": []map[string]any{
			{"name": "数据", "rows": [][]any{
				{"地市", "月份", "备注", "值"},
				{"A", "2026-10", "", 0.96},
				{"B", "2026-10", "", 0.98},
				{"A", "2026-09", "", 0.95},
				{"B", "2026-09", "", 0.97},
			}},
			{"name": "口径", "rows": [][]any{{"指标", "定义", "单位", "目标", "方向"}, {"x", "y", "z", 0.96, "高"}}},
			{"name": "看板", "header": false, "rows": [][]any{
				{"统计月份", "2026-10"},
				{
					`=AVERAGEIFS(数据!$D:$D,数据!$B:$B,$B$1)`,
					`=AVERAGEIFS(数据!$D:$D,数据!$B:$B,"2026-09")`,
					`=IFERROR(A2/B2-1,"")`,
					`=IF(A2>=口径!$D$2,"达标","未达标")`,
					`=SUMIFS(数据!$D:$D,数据!$B:$B,$B$1)`,
					`=COUNTIFS(数据!$B:$B,$B$1)`,
					`=LARGE(数据!$D$2:$D$5,1)`,
					`=INDEX(数据!$A$2:$A$5,MATCH(G2,数据!$D$2:$D$5,0))`,
					`=IFERROR(1/0,"")`,
				},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	row := readSheetRows(t, ts, "board.xlsx", "看板")[1]
	wantNumber(t, "本期 AVERAGEIFS", row[0], 0.97)
	wantNumber(t, "上期 AVERAGEIFS", row[1], 0.96)
	wantNumber(t, "较上期", row[2], 0.97/0.96-1)
	if row[3] != "达标" {
		t.Errorf("状态 = %v", row[3])
	}
	wantNumber(t, "SUMIFS", row[4], 1.94)
	wantNumber(t, "COUNTIFS", row[5], 2)
	wantNumber(t, "LARGE", row[6], 0.98)
	if row[7] != "B" {
		t.Errorf("INDEX/MATCH = %v, want B", row[7])
	}
	if row[8] != "" {
		t.Errorf("IFERROR(1/0,\"\") = %v, want an empty cell", row[8])
	}
}

func TestFinancialModelSkillFormulasCompute(t *testing.T) {
	_, ts := newXlsxTools(t)
	_, err := run(t, ToolCreateXlsx, ts, map[string]any{
		"relative_path": "model.xlsx",
		"sheets": []map[string]any{{"name": "测算", "header": false, "rows": [][]any{
			{"期号", 0, 1, 2, 3, 4, 5},
			{"现金流", -1000, 300, 300, 300, 300, 300},
			{"折现率", 0.1},
			// Recommended: a discount-factor row, the product row, then SUM.
			{"折现系数", "=1/(1+$B$3)^B1", "=1/(1+$B$3)^C1", "=1/(1+$B$3)^D1", "=1/(1+$B$3)^E1", "=1/(1+$B$3)^F1", "=1/(1+$B$3)^G1"},
			{"现值", "=B2*B4", "=C2*C4", "=D2*D4", "=E2*E4", "=F2*F4", "=G2*G4"},
			{"净现值(求和)", "=SUM(B5:G5)"},
			{"净现值(SUMPRODUCT)", "=SUMPRODUCT(B2:G2,B4:G4)"},
			// Allowed: NPV with every cash flow as its own argument.
			{"净现值(NPV)", "=B2+NPV($B$3,C2,D2,E2,F2,G2)"},
			{"IRR", "=IRR(B2:G2)"},
			{"月供", "=PMT(0.05/12,12,-1000)"},
			{"场景", "=CHOOSE(2,10,20,30)"},
			{"回收期(期数)", `=COUNTIF(B2:G2,"<0")+1`},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := readSheetRows(t, ts, "model.xlsx", "测算")
	byLabel := map[string]any{}
	for _, r := range rows {
		if len(r) > 1 {
			byLabel[r[0].(string)] = r[1]
		}
	}
	const npv = 137.23603082253 // -1000 + 300 * (1 - 1.1^-5) / 0.1
	wantNumber(t, "NPV by SUM", byLabel["净现值(求和)"], npv)
	wantNumber(t, "NPV by SUMPRODUCT", byLabel["净现值(SUMPRODUCT)"], npv)
	wantNumber(t, "NPV by NPV()", byLabel["净现值(NPV)"], npv)
	wantNumber(t, "IRR", byLabel["IRR"], 0.152382376790)
	wantNumber(t, "PMT", byLabel["月供"], 85.6074817884668)
	wantNumber(t, "CHOOSE", byLabel["场景"], 20)
	wantNumber(t, "COUNTIF", byLabel["回收期(期数)"], 2)
}
