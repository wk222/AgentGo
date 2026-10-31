package docindex

import "testing"

func TestCoverageCountsTermsTheChunkReallyHolds(t *testing.T) {
	terms := QueryTerms("区块链矿机采购流程")
	if got := Coverage(terms, "单笔采购金额超过五万元的，须提交财务处负责人复核。"); got >= 0.34 {
		t.Fatalf("a chunk sharing one word covers %.2f of the question", got)
	}
	terms = QueryTerms("超过二十万元谁批准")
	if got := Coverage(terms, "单笔采购金额超过二十万元的，须报总经理批准。"); got < 0.6 {
		t.Fatalf("the chunk that answers covers only %.2f", got)
	}
	if Coverage(nil, "anything") != 0 {
		t.Fatal("no terms, no coverage")
	}
	if got := Coverage(QueryTerms("invoice overdue"), "the invoices are overdue"); got != 1 {
		t.Fatalf("a longer Latin term must match its inflection: %.2f", got)
	}
}
