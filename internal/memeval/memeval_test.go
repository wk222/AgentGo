package memeval

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"agentgo/internal/evaluation"
	"agentgo/internal/memory"
)

var knownGaps = flag.Bool("known-gaps", false, "also run the tests that show a limit of offline recall which fails today")

const filler = "The quick review process covers many routine topics such as scheduling, approvals and general housekeeping for the whole office team."

// longDoc is six paragraphs of text shared by every document, the last of which mentions marker.
// It is longer than the 1200 bytes after which ingest cuts a document, and the marker lands in a
// later chunk than the first.
func longDoc(marker string) string {
	paras := make([]string, 6)
	for i := range paras {
		paras[i] = filler + " " + filler
	}
	paras[5] = fmt.Sprintf("Special note: %s is the only topic of this final paragraph, explained at length for every reader. %s", marker, filler)
	return strings.Join(paras, "\n\n")
}

func newBackend(t *testing.T) *Backend {
	t.Helper()
	b, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

func TestDocumentOf(t *testing.T) {
	for id, want := range map[string]string{
		"d00001":      "d00001",
		"d00001_c0":   "d00001",
		"d00001_c12":  "d00001",
		"d00001_c":    "d00001_c",
		"d00001_cab":  "d00001_cab",
		"d00001_c1x":  "d00001_c1x",
		"_c1":         "_c1",
		"a_c1_c2":     "a_c1",
		"no-suffix-c": "no-suffix-c",
	} {
		if got := documentOf(id); got != want {
			t.Errorf("documentOf(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestSearchMapsChunkHitsBackToTheirDocument(t *testing.T) {
	ctx := context.Background()
	b := newBackend(t)
	st, err := b.Index(ctx, []evaluation.CorpusDoc{
		{ID: "a/alpha.md", Text: longDoc("zeppelin")},
		{ID: "b/bravo.md", Text: longDoc("quokka")},
		{ID: "c/short.md", Text: "A short memo about marmalade."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.Docs != 3 || st.Chunks <= 3 {
		t.Fatalf("stats = %+v; want 3 documents cut into more than 3 chunks, or this test proves nothing about chunks", st)
	}
	if st.MaxChars <= 0 || st.MeanChars <= 0 || st.MaxChars < st.MeanChars {
		t.Errorf("chunk size stats are off: %+v", st)
	}

	hits, err := b.Search(ctx, "quokka", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0] != "b/bravo.md" {
		t.Fatalf("hits = %v, want b/bravo.md first", hits)
	}
	// The hit really was a later chunk, so the mapping was exercised and not bypassed.
	raw, err := b.eng.Recall(ctx, "quokka", memory.RecallOptions{Scope: scope, Limit: 5})
	if err != nil || len(raw) == 0 {
		t.Fatalf("Recall: %v %v", raw, err)
	}
	if !strings.Contains(raw[0].ID, "_c") || documentOf(raw[0].ID) != "d00002" {
		t.Errorf("top hit ID = %q, want a chunk of d00002", raw[0].ID)
	}

	hits, err = b.Search(ctx, "marmalade", 5)
	if err != nil || len(hits) == 0 || hits[0] != "c/short.md" {
		t.Errorf("short document: hits = %v, err = %v", hits, err)
	}
}

func TestSearchRefusesAHitThatBelongsToNoDocument(t *testing.T) {
	ctx := context.Background()
	b := newBackend(t)
	if _, err := b.Index(ctx, []evaluation.CorpusDoc{{ID: "a.md", Text: "alpha text about quokka habitats"}}); err != nil {
		t.Fatal(err)
	}
	stray := memory.Record{ID: "stray-1", Content: "quokka quokka quokka", Scope: scope, Modality: "fact"}
	if err := b.eng.Ingest(ctx, stray); err != nil {
		t.Fatal(err)
	}
	_, err := b.Search(ctx, "quokka", 5)
	if err == nil || !strings.Contains(err.Error(), "stray-1") {
		t.Errorf("err = %v; a hit that maps to no document must be an error, not dropped silently", err)
	}
}

func TestIndexTakesOneCorpus(t *testing.T) {
	ctx := context.Background()
	b := newBackend(t)
	docs := []evaluation.CorpusDoc{{ID: "a.md", Text: "alpha"}}
	if _, err := b.Index(ctx, docs); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Index(ctx, docs); err == nil {
		t.Error("a second Index would mix two corpora; want an error")
	}
}

func TestChunkerByNameAndSize(t *testing.T) {
	if fn, err := Chunker("bytes", 0); fn != nil || err != nil {
		t.Errorf(`Chunker("bytes", 0) = %v, %v; want nil so the default path is what runs`, fn != nil, err)
	}
	if fn, err := Chunker("", 0); fn != nil || err != nil {
		t.Errorf(`Chunker("", 0) = %v, %v; want the default`, fn != nil, err)
	}
	if _, err := Chunker("sentences", 0); err == nil {
		t.Error("unknown chunker name: want an error")
	}

	// 60 paragraphs of 13 characters. The built-in splitter cuts at blank lines and at spaces and
	// full stops that are followed by a space, so Chinese needs the blank lines to be cut at all.
	zh := strings.Repeat("员工应当按照流程提交申请。\n\n", 60)
	runes, err := Chunker("runes", 0)
	if err != nil || runes == nil {
		t.Fatalf("runes: %v %v", runes != nil, err)
	}
	for _, c := range runes(zh, 100, 0) {
		if n := utf8.RuneCountInString(c); n > 100 {
			t.Errorf("runes chunker made a chunk of %d characters, limit 100", n)
		}
	}

	// size replaces whatever ingest asks for.
	small, err := Chunker("runes", 50)
	if err != nil {
		t.Fatal(err)
	}
	pieces := small(zh, 1200, 0)
	if len(pieces) < 2 {
		t.Fatalf("size 50 gave %d pieces", len(pieces))
	}
	for _, c := range pieces {
		if n := utf8.RuneCountInString(c); n > 50 {
			t.Errorf("chunk of %d characters despite size 50", n)
		}
	}
	// Known limit, not a wish: one long Chinese paragraph with no blank line is not cut at all, so a
	// document like that is indexed as a single huge chunk. The report's "longest" figure shows it.
	oneBlock := strings.Repeat("员工应当按照流程提交申请。", 60)
	if got := small(oneBlock, 1200, 0); len(got) != 1 {
		t.Errorf("an unbroken Chinese paragraph came out as %d pieces; if the splitter learned to cut Chinese, update this test and the Chunker doc", len(got))
	}

	ascii := strings.Repeat("one short paragraph of plain text here\n\n", 40)
	bytes90, err := Chunker("bytes", 90)
	if err != nil || bytes90 == nil {
		t.Fatalf("bytes with a size: %v %v", bytes90 != nil, err)
	}
	for _, c := range bytes90(ascii, 1200, 0) {
		if len(c) > 90 {
			t.Errorf("chunk of %d bytes despite size 90", len(c))
		}
	}
}

func TestRunInstallsTheChunkerAndPutsTheOldOneBack(t *testing.T) {
	ctx := context.Background()
	suite := &evaluation.RetrievalSuite{
		Name:  "tiny",
		Docs:  []evaluation.CorpusDoc{{ID: "a.md", Text: longDoc("zeppelin")}, {ID: "b.md", Text: longDoc("quokka")}},
		Cases: []evaluation.RetrievalCase{{ID: "q1", Query: "zeppelin", Relevant: []string{"a.md"}}},
	}
	var outer, inner int
	restore := memory.SetChunker(func(c string, _, _ int) []string { outer++; return []string{c} })
	defer restore()

	// A nil chunker means the default, even while someone else has a hook installed.
	rep, err := Run(ctx, suite, nil, "default", 5)
	if err != nil {
		t.Fatal(err)
	}
	if outer != 0 {
		t.Errorf("the installed hook ran %d times during a run that asked for the default", outer)
	}
	if rep.Label != "default" || rep.Backend != Description || rep.Index == nil || rep.Index.Chunks <= rep.Index.Docs {
		t.Errorf("report = %+v, index = %+v; want label, backend and the default chunker's cuts", rep, rep.Index)
	}
	if rep.Hit != 1 {
		t.Errorf("Hit = %v, want 1", rep.Hit)
	}
	if got := memory.SplitContent("x", 1200, 0); len(got) != 1 || outer != 1 {
		t.Errorf("after Run the previous hook should be back: calls = %d", outer)
	}

	// A chunker given to Run is the one that cuts the corpus.
	perParagraph := func(c string, _, _ int) []string { inner++; return strings.Split(c, "\n\n") }
	rep, err = Run(ctx, suite, perParagraph, "paragraphs", 5)
	if err != nil {
		t.Fatal(err)
	}
	if inner != 2 || rep.Index.Chunks != 12 {
		t.Errorf("chunker calls = %d, chunks = %d; want 2 documents cut into 12 paragraphs", inner, rep.Index.Chunks)
	}
	if outer != 1 {
		t.Errorf("the previous hook ran during the second Run (calls = %d)", outer)
	}
}

func zhDocs() []evaluation.CorpusDoc {
	p := func(parts ...string) string { return strings.Join(parts, "\n\n") }
	return []evaluation.CorpusDoc{
		{ID: "finance/差旅报销制度.md", Text: p(
			"第一章 总则。为规范公司差旅费用的报销管理，提高资金使用效率，根据国家有关财务规定，结合公司实际情况，制定本制度。本制度适用于公司全体员工因公出差产生的各项费用。",
			"第二章 交通费。员工因公出差乘坐飞机、火车、长途汽车的，凭票据按实报销。乘坐飞机须选择经济舱，提前三天以上预订的，可享受折扣票价。市内交通费包括出租车费用，出租车发票必须注明起止地点和时间，否则财务部不予报销。",
			"第三章 住宿费。出差住宿费按城市级别设定标准，一线城市每晚不超过六百元，其他城市每晚不超过四百元。住宿发票抬头必须为公司全称，并附酒店结账明细单。超出标准的部分由个人承担，特殊情况须提前报部门经理批准。",
			"第四章 餐饮补贴。出差期间的餐饮费按天发放定额补贴，每人每天一百元，不再单独报销餐费发票。同行客户的接待餐费另行申请，须提供接待审批单和参加人员名单。",
			"第五章 报销流程。员工应在出差结束后三十日内提交报销申请，填写差旅费报销单，粘贴发票并经部门经理审批。金额超过五千元的报销，还须财务总监审批。逾期未报销的费用，财务部有权不予受理。",
			"第六章 借款。出差前确需借款的，应填写借款单，说明事由和金额，经部门经理批准后向财务部借支。出差结束后，须在报销时一并冲抵借款，未冲抵部分按公司规定从工资中扣回。"),
		},
		{ID: "hr/请假管理办法.md", Text: p(
			"第一章 总则。为规范员工考勤和请假管理，维护正常的工作秩序，保障员工依法享有的休息休假权利，制定本办法。本办法适用于公司全体正式员工和试用期员工。",
			"第二章 年假。员工连续工作满一年的，享受带薪年休假。工作满一年不满十年的，年假五天；满十年不满二十年的，年假十天；满二十年的，年假十五天。年假应在当年度内休完，因工作需要未休的，可顺延至次年三月底。",
			"第三章 病假。员工因病需要休息的，应提供二级以上医院出具的诊断证明和病假条。病假三天以内由部门经理批准，三天以上须报人力资源部备案。病假期间工资按基本工资的百分之八十发放。",
			"第四章 事假。员工因私事需要请假的，应提前一天向直属上级申请，经批准后方可休假。事假不发放工资，全年累计事假不得超过十五天，超过部分按旷工处理。",
			"第五章 婚假和产假。依法登记结婚的员工，享受婚假三天，晚婚的增加七天。女员工生育享受产假九十八天，其中产前休假十五天；难产的增加十五天，多胞胎生育的每多一个婴儿增加十五天。",
			"第六章 请假流程。员工请假须在办公系统中提交申请，写明请假类型、起止时间和事由，由直属上级审批。三天以上的请假还须部门负责人审批。未经批准擅自离岗的，按旷工处理。"),
		},
		{ID: "it/信息安全规范.md", Text: p(
			"第一章 总则。为保护公司信息资产安全，防止数据泄露、篡改和丢失，根据国家网络安全法律法规，制定本规范。公司全体员工、外包人员和访客均须遵守本规范。",
			"第二章 账号和密码。员工应妥善保管各类系统账号，不得与他人共用。密码长度不少于十位，须同时包含大小写字母、数字和特殊字符，并每九十天更换一次。严禁将密码写在便签上或通过即时通讯工具发送。",
			"第三章 移动存储设备。未经信息中心批准，不得将公司数据复制到U盘、移动硬盘等可移动存储设备中，更不得将含有公司数据的存储设备带出办公区域。确因工作需要的，须填写申请单并由信息中心加密后使用。",
			"第四章 远程办公。员工在家办公或出差期间访问公司内网，必须通过公司统一提供的VPN接入，并启用双因素认证。禁止使用公共电脑或公共无线网络处理涉密文件。",
			"第五章 电子邮件。员工不得打开来历不明的邮件附件，不得点击可疑链接。发现钓鱼邮件应立即转发给信息安全组，并删除原邮件。涉及客户资料的邮件须加密发送。",
			"第六章 数据备份。业务系统的数据库每天凌晨自动备份，备份文件保存三十天，每月做一次恢复演练。个人电脑上的重要文件由员工自行备份到公司网盘，信息中心定期检查。"),
		},
		{ID: "admin/会议室使用规定.md", Text: "会议室须提前在办公系统中预订，每次使用不超过两小时。使用完毕后请关闭投影仪和空调，清理桌面。"},
	}
}

// Chinese has no spaces, so the full-text index cannot split it; this checks that the engine
// still finds the right document, and that a long document's chunks map back to it, for both
// chunkers.
func TestChineseQueriesFindTheirDocument(t *testing.T) {
	queries := []struct{ q, want string }{
		{"出租车发票怎么报销", "finance/差旅报销制度.md"},
		{"出差住宿标准是多少", "finance/差旅报销制度.md"},
		{"年假可以休多少天", "hr/请假管理办法.md"},
		{"病假工资怎么发", "hr/请假管理办法.md"},
		{"U盘能不能带出公司", "it/信息安全规范.md"},
		{"密码多久换一次", "it/信息安全规范.md"},
		{"会议室怎么预订", "admin/会议室使用规定.md"},
	}
	suite := &evaluation.RetrievalSuite{Name: "zh", Docs: zhDocs()}
	for i, q := range queries {
		suite.Cases = append(suite.Cases, evaluation.RetrievalCase{ID: fmt.Sprintf("q%d", i+1), Query: q.q, Relevant: []string{q.want}})
	}
	for _, name := range []string{"bytes", "runes"} {
		chunker, err := Chunker(name, 0)
		if err != nil {
			t.Fatal(err)
		}
		rep, err := Run(context.Background(), suite, chunker, name, evaluation.DefaultK)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Hit != 1 || rep.MRR < 0.9 {
			t.Errorf("%s:\n%s", name, rep.Summary())
		}
	}
}

// The tests below exist to show something that is wrong today; see -known-gaps.

var scaleSentences = []string{
	"公司要求各部门严格执行相关规定，不得擅自变通处理",
	"员工应当按照流程提交申请，并耐心等待部门审批结果",
	"财务部负责审核各项费用，并在规定期限内按期支付款项",
	"部门经理对本部门的预算使用情况负责，每月须向公司汇报",
	"相关材料须由经办人整理归档，保存期限不少于五年时间",
	"遇到特殊情况的，应当提前书面报告，经批准后方可执行",
	"公司将定期组织检查，对违反规定的员工视情节严肃处理",
	"各项申请须在办公系统中填写，纸质材料不再单独接收",
	"项目负责人应当协调各方资源，保证工作按计划顺利推进",
	"员工对本规定有疑问的，可以向人力资源部或者财务部咨询",
}

var (
	scaleNames = []string{"青松", "红枫", "白鹭", "金鼎", "银杏", "紫荆", "翠竹", "墨兰", "碧波", "朝阳", "晨曦", "星河", "流云", "惊雷", "春雨", "秋霜", "寒梅", "暖阳", "清泉", "远山"}
	scaleKinds = []string{"计划", "项目", "工程", "行动", "方案"}
)

// scaleSuite is n office documents built from the same ten sentences, each with one paragraph
// about a project that has a name of its own, and a query for every second document that names
// the project and asks a generic question about it. A person can find the document at once: the
// project name occurs nowhere else.
func scaleSuite(n int) *evaluation.RetrievalSuite {
	rng := rand.New(rand.NewSource(42))
	s := &evaluation.RetrievalSuite{Name: fmt.Sprintf("zh-scale-%d", n)}
	used := map[string]bool{}
	for i := 0; i < n; i++ {
		var phrase string
		for phrase == "" || used[phrase] {
			phrase = scaleNames[rng.Intn(len(scaleNames))] + scaleNames[rng.Intn(len(scaleNames))] + scaleKinds[rng.Intn(len(scaleKinds))]
		}
		used[phrase] = true
		paras := make([]string, 5)
		for p := range paras {
			var sents []string
			for j := 0; j < 3; j++ {
				sents = append(sents, scaleSentences[rng.Intn(len(scaleSentences))])
			}
			paras[p] = fmt.Sprintf("第%d条 %s。", p+1, strings.Join(sents, "，"))
		}
		at := rng.Intn(len(paras))
		paras[at] = fmt.Sprintf("第%d条 本年度%s的专项预算由财务部统一审批，经办人须在系统中登记并保留全部凭证。", at+1, phrase)
		id := fmt.Sprintf("doc%03d.md", i)
		s.Docs = append(s.Docs, evaluation.CorpusDoc{ID: id, Text: strings.Join(paras, "\n\n")})
		if i%2 == 0 {
			s.Cases = append(s.Cases, evaluation.RetrievalCase{ID: "c" + id, Query: phrase + "的预算怎么审批", Relevant: []string{id}})
		}
	}
	return s
}

// TestRecallFindsARareTermAsTheCorpusGrows fails today, which is why it only runs with
// -known-gaps. Without a vector index a Chinese query is answered by SQLiteStore.RecallLikeFallback,
// which picks its candidate rows by importance and recency and only then orders them by how much
// of the query they contain. When more rows than that cap match some piece of the query, which in
// Chinese is any common two-character word, the answer is only found if it happens to be among the
// cap's rows. At k=10 the cap is 120 rows (the recall graph asks the store for 2k, and the fallback
// takes 6x that), so with equal importance and age the documents past the 120th are out of reach.
func TestRecallFindsARareTermAsTheCorpusGrows(t *testing.T) {
	if !*knownGaps {
		t.Skip("shows a known limit of offline recall; run with -known-gaps")
	}
	for _, n := range []int{40, 120, 400} {
		rep, err := Run(context.Background(), scaleSuite(n), nil, "bytes", evaluation.DefaultK)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%4d documents (%d chunks): Hit@%d %.2f, MRR %.2f", n, rep.Index.Chunks, rep.K, rep.Hit, rep.MRR)
		if rep.Hit < 1 {
			t.Errorf("%d documents: Hit@%d = %.2f; a query naming a project that occurs in one document only should find it", n, rep.K, rep.Hit)
		}
	}
}
