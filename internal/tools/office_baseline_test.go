package tools

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentgo/internal/evaluation"
	"agentgo/internal/governance"
)

// The office baseline: 18 tasks over one small workspace of the files an office
// actually has (Chinese policies, a workbook, a PDF, a long text). Each task asks
// a question the way the model would, then grades what the index put in front of it:
//
//   - support:  a hit from the right file contains every fact the answer needs;
//   - citation: the hit's citation reads back, at the cited range and revision, to
//     the very text of the hit, and carries the page / sheet / row it came from;
//   - no-answer: a question the files cannot answer gets no hit at all;
//   - leak:     text of a superseded, deleted or stale file never reaches the model.
//
// Some tasks edit or delete files first, so the same corpus is asked again after it
// changed ("modify / delete / re-ask"). The suite needs no model and no network:
// "model calls" is 0 by construction, and what it costs is searches and readbacks.
// It is a regression gate for the evidence layer, not a score of any model.
//
// Write the report for a before/after comparison:
//
//	go test ./internal/tools -run TestOfficeBaseline -args -office-baseline-out=report.json
var officeBaselineOut = flag.String("office-baseline-out", "", "write the office baseline report (JSON) here")

type officeExpect struct {
	Path    string   // the file the answer must come from
	Facts   []string // every one must be in the supporting hit
	Locator string   // must be in the hit's locator ("page 2", `sheet "部门预算"`)
	// ContextFacts must be in the hit's neighbours, not the hit: the answer needs both sides.
	ContextFacts []string
	None         bool     // the files cannot answer; any hit is a wrong answer
	Forbid       []string // text that must not appear in any hit
	Stale        bool     // the file must be reported as changed, and none of it returned
	DeadCitation string   // the id of an earlier task whose citation must no longer read back
}

type officeTask struct {
	ID      string
	Kind    string
	Query   string
	Context int
	Pre     func(e *indexEnv)
	Exp     officeExpect
}

type officeResult struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Query     string  `json:"query"`
	Pass      bool    `json:"pass"`
	Support   bool    `json:"support"`
	Citation  bool    `json:"citation"`
	Leak      bool    `json:"leak"`
	Hits      int     `json:"hits"`
	Rank      int     `json:"rank"`
	Searches  int     `json:"searches"`
	Readbacks int     `json:"readbacks"`
	Millis    float64 `json:"ms"`
	Why       string  `json:"why,omitempty"`
}

type officeReport struct {
	Suite       string                 `json:"suite"`
	Tasks       int                    `json:"tasks"`
	Passed      int                    `json:"passed"`
	ModelCalls  int                    `json:"model_calls"`
	Searches    int                    `json:"searches"`
	Readbacks   int                    `json:"readbacks"`
	Millis      float64                `json:"ms"`
	Hit         float64                `json:"hit_at_k"`
	MRR         float64                `json:"mrr"`
	ByKind      map[string][2]int      `json:"by_kind_passed_total"`
	Leaks       int                    `json:"leaks"`
	Results     []officeResult         `json:"results"`
	LookupScore []evaluation.CaseScore `json:"lookup_scores,omitempty"`
}

func officePolicyCorpus(e *indexEnv) {
	e.write("policy/travel_2025.md", []byte("# 差旅制度（2025版）\n\n出差住宿标准：一线城市每晚不超过 600 元。\n"))
	e.write("policy/travel_2026.md", []byte("# 差旅制度（2026版）\n\n出差住宿标准：一线城市每晚不超过 800 元；二线城市每晚不超过 500 元。\n"))
	e.write("policy/procurement.txt", []byte("采购管理办法\n\n单笔采购金额超过五万元的，须提交财务处负责人复核。\n\n单笔采购金额超过二十万元的，须报总经理批准。\n"))
	e.write("policy/leave.md", []byte("# 休假制度\n\n年假：工作满一年不满十年的员工，每年享受五天带薪年假。\n\n病假：须提供二级以上医院证明。\n"))
	// The condition is the last line of one chunk and the conclusion the first of the next:
	// a search finds the condition, and only the neighbour says what happens.
	filler := strings.Repeat("本章条款另有说明，参见附录。", 20) // ~280 runes
	cond := "报销时限：出差报销单须在返程后十个工作日内提交财务处。" + strings.Repeat("时限自返程当日起算。", 14)
	concl := "逾期后果：超过时限提交的报销单一律不予报销，由个人承担。" + strings.Repeat("特殊情况须总经理书面批准。", 12)
	e.write("policy/expense_rules.txt", []byte(filler+"\n"+cond+"\n"+concl+"\n"))
	e.writeXLSX("finance/budget.xlsx", map[string][][]any{"部门预算": {
		{"部门", "年度预算", "负责人"},
		{"研发部", 1200000, "陈工"},
		{"市场部", 800000, "李经理"},
		{"行政部", 300000, "王主任"},
	}})
	e.write("docs/contract.pdf", buildTestPDF([]string{
		"Page one covers definitions and parties only.",
		"The supplier grants a twenty four months warranty period from the delivery date.",
		"Termination requires ninety days written notice from either party.",
	}))
}

func officeTasks() []officeTask {
	rm := func(e *indexEnv, rel string) {
		e.t.Helper()
		if err := os.Remove(filepath.Join(e.root, filepath.FromSlash(rel))); err != nil {
			e.t.Fatal(err)
		}
	}
	return []officeTask{
		{ID: "xlsx-1", Kind: "lookup", Query: "研发部 年度预算", Exp: officeExpect{Path: "finance/budget.xlsx", Facts: []string{"1200000"}, Locator: `sheet "部门预算"`}},
		{ID: "xlsx-2", Kind: "lookup", Query: "市场部 负责人", Exp: officeExpect{Path: "finance/budget.xlsx", Facts: []string{"李经理"}, Locator: `sheet "部门预算"`}},
		{ID: "zh-1", Kind: "lookup", Query: "采购超过五万元怎么审批", Exp: officeExpect{Path: "policy/procurement.txt", Facts: []string{"财务处负责人复核"}}},
		{ID: "zh-2", Kind: "lookup", Query: "超过二十万元谁批准", Exp: officeExpect{Path: "policy/procurement.txt", Facts: []string{"总经理批准"}}},
		{ID: "zh-3", Kind: "lookup", Query: "二线城市住宿标准", Exp: officeExpect{Path: "policy/travel_2026.md", Facts: []string{"二线城市每晚不超过 500 元"}}},
		{ID: "pdf-1", Kind: "lookup", Query: "warranty period", Exp: officeExpect{Path: "docs/contract.pdf", Facts: []string{"twenty four months"}, Locator: "page 2"}},
		{ID: "pdf-2", Kind: "lookup", Query: "termination written notice", Exp: officeExpect{Path: "docs/contract.pdf", Facts: []string{"ninety days"}, Locator: "page 3"}},
		{ID: "ctx-1", Kind: "context", Query: "报销单须在返程后几个工作日内提交", Context: 1,
			Exp: officeExpect{Path: "policy/expense_rules.txt", Facts: []string{"返程后十个工作日"}, ContextFacts: []string{"一律不予报销"}}},
		{ID: "none-1", Kind: "no_answer", Query: "区块链矿机采购流程", Exp: officeExpect{None: true}},
		{ID: "none-2", Kind: "no_answer", Query: "stock option vesting schedule", Exp: officeExpect{None: true}},
		{ID: "mod-1", Kind: "modify", Query: "单笔采购金额超过", Exp: officeExpect{Stale: true, Path: "policy/procurement.txt", Forbid: []string{"超过十万元"}},
			Pre: func(e *indexEnv) {
				e.write("policy/procurement.txt", []byte("采购管理办法\n\n单笔采购金额超过十万元的，须提交财务处负责人复核。\n\n单笔采购金额超过二十万元的，须报总经理批准。\n"))
			}},
		{ID: "mod-2", Kind: "modify", Query: "采购超过十万元怎么审批", Exp: officeExpect{Path: "policy/procurement.txt", Facts: []string{"超过十万元", "财务处负责人复核"}, Forbid: []string{"超过五万元"}, DeadCitation: "zh-1"},
			Pre: func(e *indexEnv) { e.refresh("policy/procurement.txt") }},
		{ID: "mod-3", Kind: "modify", Query: "warranty period", Exp: officeExpect{Path: "docs/contract.pdf", Facts: []string{"thirty six months"}, Locator: "page 2", Forbid: []string{"twenty four months"}, DeadCitation: "pdf-1"},
			Pre: func(e *indexEnv) {
				e.write("docs/contract.pdf", buildTestPDF([]string{
					"Page one covers definitions and parties only.",
					"The supplier grants a thirty six months warranty period from the delivery date.",
					"Termination requires ninety days written notice from either party.",
				}))
				e.refresh("docs/contract.pdf")
			}},
		{ID: "del-1", Kind: "delete", Query: "年假 每年享受几天", Exp: officeExpect{None: true, Forbid: []string{"五天带薪年假"}},
			Pre: func(e *indexEnv) {
				rm(e, "policy/leave.md")
				if out := e.sync(false, "policy"); out.Sync.Dropped != 1 {
					e.t.Fatalf("sync should drop exactly the deleted file: %+v", out.Sync)
				}
			}},
		{ID: "del-2", Kind: "delete", Query: "一线城市住宿标准", Exp: officeExpect{Path: "policy/travel_2026.md", Facts: []string{"800 元"}, Forbid: []string{"600 元"}},
			Pre: func(e *indexEnv) {
				rm(e, "policy/travel_2025.md")
				e.sync(false, "policy")
			}},
		{ID: "add-1", Kind: "add", Query: "出差补助 每天", Exp: officeExpect{Path: "policy/allowance_2027.md", Facts: []string{"每天 100 元"}},
			Pre: func(e *indexEnv) {
				e.write("policy/allowance_2027.md", []byte("# 补助办法\n\n出差补助标准：每天 100 元，按实际出差天数发放。\n"))
				e.sync(false, "policy")
			}},
		{ID: "safe-1", Kind: "safety", Query: "研发部 年度预算", Exp: officeExpect{Path: "finance/budget.xlsx", Facts: []string{"1200000"}, Locator: `sheet "部门预算"`},
			Pre: func(e *indexEnv) {
				// A folder that is briefly gone (a dropped network share, a half-finished sync
				// of another tool) must not wipe the index: the files come back and still answer.
				saved, err := os.ReadFile(filepath.Join(e.root, "finance", "budget.xlsx"))
				if err != nil {
					e.t.Fatal(err)
				}
				if err := os.RemoveAll(filepath.Join(e.root, "finance")); err != nil {
					e.t.Fatal(err)
				}
				if out, err := e.d.index(e.ctx, indexDocumentsInput{Action: "sync", Paths: []string{"finance"}}); err == nil && out.Sync != nil && out.Sync.Dropped > 0 {
					e.t.Fatalf("a missing folder dropped %d files from the index", out.Sync.Dropped)
				}
				e.write("finance/budget.xlsx", saved)
			}},
		{ID: "ver-1", Kind: "version", Query: "差旅 二线城市", Exp: officeExpect{Path: "policy/travel_2026.md", Facts: []string{"500 元"}, Forbid: []string{"2025版"}}},
	}
}

func TestOfficeBaseline(t *testing.T) {
	e := newIndexEnv(t)
	officePolicyCorpus(e)
	if out := e.add("policy", "finance", "docs"); out.Failed != 0 || out.Indexed != 7 {
		t.Fatalf("indexing the corpus: %+v", out)
	}
	const k = 6
	kept := map[string]docHit{}
	rep := officeReport{Suite: "office-baseline-v1", ModelCalls: 0, ByKind: map[string][2]int{}}
	var hitsSum, mrrSum float64
	var lookups int

	for _, task := range officeTasks() {
		if task.Pre != nil {
			task.Pre(e)
		}
		e.ctx = WithEvidenceRun(governance.WithSessionID(context.Background(), "s1")) // one run per question, as in the agent
		res := officeResult{ID: task.ID, Kind: task.Kind, Query: task.Query, Searches: 1}
		start := time.Now()
		out, err := e.d.search(e.ctx, searchDocumentsInput{Query: task.Query, Limit: k, Context: task.Context})
		if err != nil {
			t.Fatalf("%s: %v", task.ID, err)
		}
		res.Hits = len(out.Hits)
		gradeOffice(e, task, out, kept, &res)
		res.Millis = float64(time.Since(start).Microseconds()) / 1000
		rep.Results = append(rep.Results, res)
		rep.Searches += res.Searches
		rep.Readbacks += res.Readbacks
		rep.Millis += res.Millis
		if res.Leak {
			rep.Leaks++
		}
		if res.Pass {
			rep.Passed++
		}
		bk := rep.ByKind[task.Kind]
		bk[1]++
		if res.Pass {
			bk[0]++
		}
		rep.ByKind[task.Kind] = bk
		if !task.Exp.None && !task.Exp.Stale && task.Exp.Path != "" {
			var paths []string
			for _, h := range out.Hits {
				paths = append(paths, h.Path)
			}
			sc := evaluation.ScoreHits(paths, []string{task.Exp.Path}, k)
			sc.ID, sc.Query = task.ID, task.Query
			rep.LookupScore = append(rep.LookupScore, sc)
			hitsSum += sc.Hit
			mrrSum += sc.RR
			lookups++
		}
		if !res.Pass {
			t.Errorf("%s (%s) %q: %s", task.ID, task.Kind, task.Query, res.Why)
		}
	}
	rep.Tasks = len(rep.Results)
	if lookups > 0 {
		rep.Hit, rep.MRR = hitsSum/float64(lookups), mrrSum/float64(lookups)
	}
	t.Logf("office baseline: %d/%d passed, hit@%d %.2f, mrr %.2f, %d searches, %d readbacks, %.1f ms, %d leaks, model calls %d",
		rep.Passed, rep.Tasks, k, rep.Hit, rep.MRR, rep.Searches, rep.Readbacks, rep.Millis, rep.Leaks, rep.ModelCalls)
	if rep.Tasks < 12 || rep.Tasks > 20 {
		t.Fatalf("the baseline is meant to be 12-20 tasks, got %d", rep.Tasks)
	}
	if rep.Leaks != 0 {
		t.Fatalf("%d tasks leaked superseded or deleted text", rep.Leaks)
	}
	if *officeBaselineOut != "" {
		buf, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(*officeBaselineOut, buf, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func gradeOffice(e *indexEnv, task officeTask, out searchDocumentsOutput, kept map[string]docHit, res *officeResult) {
	exp := task.Exp
	fail := func(format string, a ...any) {
		if res.Why == "" {
			res.Why = fmt.Sprintf(format, a...)
		}
	}
	// What the model would be shown, neighbours included.
	var all strings.Builder
	var walk func(h docHit)
	walk = func(h docHit) {
		all.WriteString(h.Text)
		all.WriteString("\n")
		for _, n := range h.Before {
			walk(n)
		}
		for _, n := range h.After {
			walk(n)
		}
	}
	for _, h := range out.Hits {
		walk(h)
	}
	for _, f := range exp.Forbid {
		if strings.Contains(all.String(), f) {
			res.Leak = true
			fail("the answer contains %q, which is superseded or deleted", f)
		}
	}
	switch {
	case exp.None:
		// No hit, or hits the tool itself flags as a weak match, is "the files do not say".
		if len(out.Hits) != 0 && !out.Weak {
			fail("expected no answer, got %d unflagged hits (first: %s)", len(out.Hits), out.Hits[0].Path)
		}
		res.Support, res.Citation = res.Why == "", true
	case exp.Stale:
		reported := false
		for _, s := range out.Stale {
			if s.Path == exp.Path {
				reported = true
			}
		}
		for _, h := range out.Hits {
			if h.Path == exp.Path {
				fail("%s changed on disk but its old text was returned", exp.Path)
				res.Leak = true
			}
		}
		if !reported {
			fail("%s changed on disk and was not reported as stale", exp.Path)
		}
		res.Support, res.Citation = res.Why == "", true
	default:
		for i, h := range out.Hits {
			if h.Path != exp.Path || !containsAll(h.Text, exp.Facts) {
				continue
			}
			res.Rank = i + 1
			if out.Weak {
				fail("a real answer was flagged as a weak match: %s", out.Message)
			}
			res.Support = containsAll(neighbourText(h), exp.ContextFacts)
			if !res.Support {
				fail("the neighbours do not carry %v", exp.ContextFacts)
			}
			kept[task.ID] = h
			res.Citation = officeCitationReads(e, h, exp.Locator, res)
			if !res.Citation {
				fail("the citation of %s does not read back to the hit", h.Path)
			}
			break
		}
		if res.Rank == 0 {
			fail("no hit from %s carries %v (hits: %s)", exp.Path, exp.Facts, hitPaths(out))
		}
	}
	if exp.DeadCitation != "" {
		old, ok := kept[exp.DeadCitation]
		res.Readbacks++
		if !ok {
			fail("task %s kept no citation to check", exp.DeadCitation)
		} else if _, err := readParsedFile(e.ctx, e.root, parseFileInput{RelativePath: old.Path, Offset: old.Citation.Start,
			Limit: old.Citation.End - old.Citation.Start, ExpectedRevision: old.Citation.Revision}); err == nil {
			fail("the citation of the old version still reads: a change would go unnoticed")
		}
	}
	res.Pass = res.Why == ""
}

func officeCitationReads(e *indexEnv, h docHit, locator string, res *officeResult) bool {
	if h.Citation == nil || h.Citation.Revision == "" {
		return false
	}
	if locator != "" && !strings.Contains(h.Locator, locator) {
		return false
	}
	res.Readbacks++
	pf, err := readParsedFile(e.ctx, e.root, parseFileInput{
		RelativePath: h.Path, Offset: h.Citation.Start, Limit: h.Citation.End - h.Citation.Start, ExpectedRevision: h.Citation.Revision,
	})
	return err == nil && pf.Text == h.Text && pf.Source.Revision == h.Citation.Revision
}

func neighbourText(h docHit) string {
	var b strings.Builder
	for _, n := range append(append([]docHit{}, h.Before...), h.After...) {
		b.WriteString(n.Text)
		b.WriteString("\n")
	}
	return b.String()
}

func containsAll(s string, parts []string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
