package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"agentgo/internal/governance"
	"agentgo/internal/tools"

	"github.com/xuri/excelize/v2"
)

// A small office-document evaluation of the document index: files go in, a
// question set is asked, and the answers are scored for where the evidence is
// found (document, page or sheet and row), whether the excerpts hold everything
// the question needs, whether every citation resolves to the text parse_file
// returns for the same range, and what happens after a document is edited or
// deleted. It runs the index with the built-in splitter and with the adaptive
// chunker, and with and without neighbouring excerpts, so the effect of each
// can be read as a number rather than argued.
//
// What it does not score is the model's wording of an answer: that needs a
// model, and these tests run without one. The score is for the evidence the
// model would be given.

type evalQuestion struct {
	ID      string
	Kind    string // single, split (conclusion on the next page), table
	Query   string
	Path    string
	Page    int    // PDF page the first needle sits on, 0 when not a PDF
	Sheet   string // workbook sheet and the row the first needle sits on
	Row     int
	Needles []string // everything an answer needs; the first is the key fact
}

type evalHit struct {
	Path     string `json:"path"`
	Locator  string `json:"locator"`
	Text     string `json:"text"`
	Citation *struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
		Start    int    `json:"start"`
		End      int    `json:"end"`
	} `json:"citation"`
	Before []evalHit `json:"before"`
	After  []evalHit `json:"after"`
}

type evalSearch struct {
	Hits    []evalHit                      `json:"hits"`
	Stale   []struct{ Path, State string } `json:"stale"`
	Message string                         `json:"message"`
}

func evalPDF(pages []string) []byte {
	var buf bytes.Buffer
	var offsets []int
	obj := func(body string) {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	buf.WriteString("%PDF-1.4\n")
	kids := make([]string, len(pages))
	for i := range pages {
		kids[i] = fmt.Sprintf("%d 0 R", 4+2*i)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages)))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	for i, text := range pages {
		stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*i))
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return buf.Bytes()
}

// fillers are neutral sentences that pad a section to a realistic length
// without repeating the words the questions use.
var evalFillers = []string{
	"本条款自发布之日起执行，如有调整以公司最新通知为准。",
	"各部门应组织员工认真学习，并在日常工作中严格遵守。",
	"对制度执行过程中发现的问题，请及时向行政部门反馈。",
	"制度的解释权归公司管理委员会，修订时应征求工会意见。",
	"新入职员工应在入职培训中了解本制度的主要内容。",
	"相关表单可在内部办公系统的知识库栏目下载使用。",
}

func evalSection(title, fact string, pad int) string {
	var b strings.Builder
	b.WriteString("## " + title + "\n\n" + fact + "\n\n")
	for i := 0; i < pad; i++ {
		b.WriteString(evalFillers[(i+len(title))%len(evalFillers)])
	}
	return b.String() + "\n\n"
}

func evalTravelPolicy(reimburseDays string) string {
	return "# 差旅管理制度\n\n" +
		evalSection("第一章 总则", "本制度适用于全体员工的国内出差。出差前须提交出差申请单，经部门负责人审批。", 6) +
		evalSection("第二章 住宿标准", "一线城市住宿费每晚不超过600元，二线城市每晚不超过400元，其他城市每晚不超过300元。", 6) +
		evalSection("第三章 报销流程", "出差报销单必须在返程后"+reimburseDays+"内提交财务部审核。逾期提交的，须由部门总监书面说明原因。", 6) +
		evalSection("第四章 交通与补助", "飞机经济舱须提前7天预订，否则超出部分自理。出差补助标准为每天120元。", 6)
}

func evalLeavePolicy() string {
	return "# 请假管理制度\n\n" +
		evalSection("第一章 年假", "工龄满1年不满10年的员工每年享有5天年假，满10年不满20年的享有10天，满20年的享有15天。", 6) +
		evalSection("第二章 病假", "病假须在返岗后三日内补交医院出具的证明，并经部门负责人审批。", 6) +
		evalSection("第三章 其他假期", "婚假3天，产假按国家规定执行，丧假按直系亲属关系给予1至3天。", 6)
}

var evalProcurementPages = []string{
	"Section one introduces the purchasing handbook of the company. It explains the purpose of the handbook and who must follow it when buying goods or services for any department.",
	"Purchases above 500 yuan require director approval provided that",
	"the request is filed within 30 days of the purchase date. Purchases above 5000 yuan additionally require",
	"a written comparison of three vendor quotes before any order is placed. Emergency purchases are reported afterwards.",
	"Vendor onboarding needs a tax certificate and a signed code of conduct from the supplier before the first order. The finance team keeps the vendor file.",
	"Section six lists the contacts of the procurement office and the opening hours of the service desk during the working week.",
}

func evalBudgetBook() (data []byte, marketingRow int) {
	f := excelize.NewFile()
	_ = f.SetSheetName("Sheet1", "Budget2025")
	_, _ = f.NewSheet("Vendors")
	put := func(sheet string, row int, vals ...any) {
		for c, v := range vals {
			cell, _ := excelize.CoordinatesToCellName(c+1, row)
			_ = f.SetCellValue(sheet, cell, v)
		}
	}
	put("Budget2025", 1, "Department", "Q1", "Q2", "Q3", "Q4")
	depts := []string{"Engineering", "Marketing", "Finance", "Operations", "Legal", "Support", "Research", "Facilities"}
	for i, d := range depts {
		put("Budget2025", i+2, d, 1000000+i*50000, 1100000+i*50000, 1200000+i*50000+i, 1300000+i*50000)
		if d == "Marketing" {
			marketingRow = i + 2
		}
	}
	put("Vendors", 1, "Vendor", "Contact", "Rating")
	put("Vendors", 2, "Acme Supplies", "orders@acme.example", "A")
	put("Vendors", 3, "Northwind Traders", "sales@northwind.example", "B")
	put("Vendors", 4, "Globex Office", "service@globex.example", "B")
	buf, _ := f.WriteToBuffer()
	return buf.Bytes(), marketingRow
}

type evalEnv struct {
	t   *testing.T
	rt  *Runtime
	ctx context.Context
	ws  string
	qs  []evalQuestion
}

func newEvalEnv(t *testing.T) *evalEnv {
	t.Helper()
	rt := boot(t)
	s := NewAppService(rt)
	t.Cleanup(s.Close)
	e := &evalEnv{t: t, rt: rt, ws: rt.workspace}
	e.ctx = tools.WithEvidenceRun(governance.WithSessionID(context.Background(), "eval"))
	write := func(rel string, data []byte) {
		p := filepath.Join(e.ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("hr/差旅制度.md", []byte(evalTravelPolicy("30日")))
	write("hr/请假制度.md", []byte(evalLeavePolicy()))
	write("finance/procurement.pdf", evalPDF(evalProcurementPages))
	book, marketingRow := evalBudgetBook()
	write("finance/budget.xlsx", book)
	e.qs = []evalQuestion{
		{ID: "stay", Kind: "single", Query: "出差住宿一线城市每晚多少钱", Path: "hr/差旅制度.md", Needles: []string{"600元"}},
		{ID: "deadline", Kind: "single", Query: "出差报销单什么时候提交", Path: "hr/差旅制度.md", Needles: []string{"30日"}},
		{ID: "late", Kind: "single", Query: "逾期提交报销怎么办", Path: "hr/差旅制度.md", Needles: []string{"部门总监"}},
		{ID: "flight", Kind: "single", Query: "机票要提前几天预订", Path: "hr/差旅制度.md", Needles: []string{"7天"}},
		{ID: "allowance", Kind: "single", Query: "出差补助每天多少", Path: "hr/差旅制度.md", Needles: []string{"120元"}},
		{ID: "annual", Kind: "single", Query: "工龄满20年年假有几天", Path: "hr/请假制度.md", Needles: []string{"15天"}},
		{ID: "sick", Kind: "single", Query: "病假要补交什么证明", Path: "hr/请假制度.md", Needles: []string{"医院出具的证明"}},
		{ID: "pdf-vendor", Kind: "single", Query: "vendor onboarding tax certificate", Path: "finance/procurement.pdf", Page: 5, Needles: []string{"tax certificate"}},
		{ID: "pdf-split-500", Kind: "split", Query: "purchases above 500 yuan director approval", Path: "finance/procurement.pdf", Page: 2, Needles: []string{"director approval", "30 days"}},
		{ID: "pdf-split-5000", Kind: "split", Query: "purchases above 5000 yuan additionally require", Path: "finance/procurement.pdf", Page: 3, Needles: []string{"5000 yuan", "three vendor quotes"}},
		{ID: "xlsx-budget", Kind: "table", Query: "Marketing budget", Path: "finance/budget.xlsx", Sheet: "Budget2025", Row: marketingRow, Needles: []string{"Marketing", "1250001"}},
		{ID: "xlsx-vendor", Kind: "table", Query: "Acme Supplies contact email", Path: "finance/budget.xlsx", Sheet: "Vendors", Row: 2, Needles: []string{"orders@acme.example"}},
	}
	return e
}

func (e *evalEnv) call(name string, args map[string]any, out any) {
	e.t.Helper()
	raw, _ := json.Marshal(args)
	res, err := e.rt.toolReg.InvokeJSON(e.ctx, name, string(raw))
	if err != nil {
		e.t.Fatalf("%s %s: %v", name, raw, err)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(res), out); err != nil {
			e.t.Fatalf("%s: bad result %q: %v", name, res, err)
		}
	}
}

func (e *evalEnv) search(q string, context, limit int) evalSearch {
	var out evalSearch
	e.call("search_documents", map[string]any{"query": q, "limit": limit, "context": context}, &out)
	return out
}

var evalRows = regexp.MustCompile(`sheet "([^"]+)" rows? (\d+)(?:-(\d+))?`)

func locatorCovers(locator string, q evalQuestion) bool {
	switch {
	case q.Page > 0:
		return strings.Contains(locator, "page "+strconv.Itoa(q.Page)) && !strings.Contains(locator, "pages")
	case q.Sheet != "":
		for _, m := range evalRows.FindAllStringSubmatch(locator, -1) {
			lo, _ := strconv.Atoi(m[2])
			hi := lo
			if m[3] != "" {
				hi, _ = strconv.Atoi(m[3])
			}
			if m[1] == q.Sheet && lo <= q.Row && q.Row <= hi {
				return true
			}
		}
		return false
	}
	return true
}

// union is the text an answer could draw on from this document: every returned
// excerpt, and the neighbours attached to them.
func evalUnion(hits []evalHit, path string) string {
	var b strings.Builder
	var add func(h evalHit)
	add = func(h evalHit) {
		if h.Path == path {
			b.WriteString(h.Text + "\n")
		}
		for _, n := range h.Before {
			add(n)
		}
		for _, n := range h.After {
			add(n)
		}
	}
	for _, h := range hits {
		add(h)
	}
	return b.String()
}

type evalScore struct {
	N                             int
	Hit1, Hit3, Complete3, CiteOK int
	Cites                         int
	SplitN, SplitComplete         int
}

func (s evalScore) String() string {
	pct := func(a, b int) string {
		if b == 0 {
			return "n/a"
		}
		return fmt.Sprintf("%3.0f%%", 100*float64(a)/float64(b))
	}
	return fmt.Sprintf("hit@1 %s  hit@3 %s  complete@3 %s  split-complete %s  citations-valid %s (%d)",
		pct(s.Hit1, s.N), pct(s.Hit3, s.N), pct(s.Complete3, s.N), pct(s.SplitComplete, s.SplitN), pct(s.CiteOK, s.Cites), s.Cites)
}

// verifyCitation reads the cited range back through parse_file and requires the
// same text, revision and citation id.
func (e *evalEnv) verifyCitation(h evalHit) bool {
	if h.Citation == nil {
		return false
	}
	var pf struct {
		Text     string                    `json:"text"`
		Source   struct{ Revision string } `json:"source"`
		Citation *struct{ ID string }      `json:"citation"`
	}
	e.call("parse_file", map[string]any{
		"relative_path": h.Path, "offset": h.Citation.Start, "limit": h.Citation.End - h.Citation.Start, "expected_revision": h.Citation.Revision,
	}, &pf)
	return pf.Text == h.Text && pf.Source.Revision == h.Citation.Revision && pf.Citation != nil && pf.Citation.ID == h.Citation.ID
}

func (e *evalEnv) score(context, limit int) evalScore {
	var s evalScore
	for _, q := range e.qs {
		res := e.search(q.Query, context, limit)
		s.N++
		first := func(h evalHit) bool {
			return h.Path == q.Path && locatorCovers(h.Locator, q) && strings.Contains(h.Text, q.Needles[0])
		}
		for i, h := range res.Hits {
			if first(h) {
				if i == 0 {
					s.Hit1++
				}
				s.Hit3++
				break
			}
		}
		union := evalUnion(res.Hits, q.Path)
		complete := true
		for _, n := range q.Needles {
			if !strings.Contains(union, n) {
				complete = false
			}
		}
		if complete {
			s.Complete3++
		}
		if q.Kind == "split" {
			s.SplitN++
			if complete {
				s.SplitComplete++
			}
		}
		var each func(h evalHit)
		each = func(h evalHit) {
			s.Cites++
			if e.verifyCitation(h) {
				s.CiteOK++
			} else {
				e.t.Logf("citation does not resolve: %s %s %+v", q.ID, h.Path, h.Citation)
			}
			for _, n := range h.Before {
				each(n)
			}
			for _, n := range h.After {
				each(n)
			}
		}
		for _, h := range res.Hits {
			each(h)
		}
		if complete && context == 0 {
			continue
		}
		if !complete {
			e.t.Logf("  incomplete [%s] %q: hits=%d", q.ID, q.Query, len(res.Hits))
		}
	}
	return s
}

func TestDocumentIndexOfficeEval(t *testing.T) {
	type variant struct{ name, chunker string }
	for _, v := range []variant{{"builtin-splitter", ""}, {"adaptive-chunker", "1"}} {
		t.Run(v.name, func(t *testing.T) {
			t.Setenv("AGENTGO_DOCCHUNK", v.chunker)
			e := newEvalEnv(t)
			var idx struct {
				Indexed, Failed int
				Message         string
			}
			e.call("index_documents", map[string]any{"action": "add", "paths": []string{"hr", "finance"}}, &idx)
			if idx.Indexed != 4 || idx.Failed != 0 {
				t.Fatalf("index: %+v", idx)
			}
			// A model that reads only the best excerpt, then the best three.
			no1, with1 := e.score(0, 1), e.score(1, 1)
			no3, with3 := e.score(0, 3), e.score(1, 3)
			t.Logf("%s, best 1, no neighbours:    %s", v.name, no1)
			t.Logf("%s, best 1, 1 neighbour each: %s", v.name, with1)
			t.Logf("%s, best 3, no neighbours:    %s", v.name, no3)
			t.Logf("%s, best 3, 1 neighbour each: %s", v.name, with3)

			for _, s := range []evalScore{no1, with1, no3, with3} {
				if s.CiteOK != s.Cites {
					t.Errorf("every citation must resolve to the text parse_file returns for that range: %d/%d", s.CiteOK, s.Cites)
				}
			}
			if no3.Hit3 < no3.N*9/10 {
				t.Errorf("hit@3 = %d/%d, want at least 90%%", no3.Hit3, no3.N)
			}
			if with1.SplitComplete != with1.SplitN {
				t.Errorf("with one neighbour each, the best excerpt alone must answer every split question: %d/%d", with1.SplitComplete, with1.SplitN)
			}
			if with1.Complete3 < no1.Complete3 || with3.Complete3 < no3.Complete3 {
				t.Errorf("neighbours must never lose evidence")
			}
			if with1.SplitComplete <= no1.SplitComplete {
				t.Errorf("the split questions must need the neighbours at best-1 (%d without, %d with); if not, the eval no longer measures them", no1.SplitComplete, with1.SplitComplete)
			}
		})
	}
}

// Edit a document, then ask again: the old answer must not come back, the
// edited document must be reported, and after a refresh the new answer must be
// found with a citation to the new version. Delete a document: same.
func TestDocumentIndexEvalEditAndDelete(t *testing.T) {
	e := newEvalEnv(t)
	e.call("index_documents", map[string]any{"action": "add", "paths": []string{"hr", "finance"}}, nil)
	ask := func() evalSearch { return e.search("出差报销单什么时候提交", 0, 3) }
	has := func(r evalSearch, s string) bool { return strings.Contains(evalUnion(r.Hits, "hr/差旅制度.md"), s) }

	before := ask()
	if !has(before, "30日") || len(before.Hits) == 0 {
		t.Fatalf("setup: %+v", before)
	}
	oldRev := before.Hits[0].Citation.Revision

	if err := os.WriteFile(filepath.Join(e.ws, "hr", "差旅制度.md"), []byte(evalTravelPolicy("15日")), 0o644); err != nil {
		t.Fatal(err)
	}
	edited := ask()
	if has(edited, "30日") || has(edited, "15日") {
		t.Fatalf("an edited document must not answer from the index before it is refreshed: %+v", edited)
	}
	if len(edited.Stale) != 1 || edited.Stale[0].Path != "hr/差旅制度.md" {
		t.Fatalf("the edit must be reported: %+v", edited.Stale)
	}
	for _, h := range edited.Hits {
		if h.Path == "hr/差旅制度.md" {
			t.Fatalf("stale document returned: %+v", h)
		}
	}

	var ref struct{ Updated int }
	e.call("index_documents", map[string]any{"action": "refresh"}, &ref)
	if ref.Updated != 1 {
		t.Fatalf("refresh: %+v", ref)
	}
	after := ask()
	if !has(after, "15日") || has(after, "30日") || len(after.Stale) != 0 {
		t.Fatalf("after refresh the new answer, and only it: %+v", after)
	}
	if after.Hits[0].Citation.Revision == oldRev {
		t.Fatal("the edited document must be cited at a new revision")
	}
	if !e.verifyCitation(after.Hits[0]) {
		t.Fatal("the new citation must resolve")
	}

	if err := os.Remove(filepath.Join(e.ws, "hr", "请假制度.md")); err != nil {
		t.Fatal(err)
	}
	gone := e.search("工龄满20年年假有几天", 0, 3)
	if strings.Contains(evalUnion(gone.Hits, "hr/请假制度.md"), "15天") {
		t.Fatalf("a deleted document must not answer: %+v", gone)
	}
	e.call("index_documents", map[string]any{"action": "refresh"}, &ref)
	var list struct {
		Total    int
		Outdated int
	}
	e.call("list_indexed_documents", map[string]any{}, &list)
	if list.Total != 3 || list.Outdated != 0 {
		t.Fatalf("after refresh the index holds the three remaining documents: %+v", list)
	}
}
