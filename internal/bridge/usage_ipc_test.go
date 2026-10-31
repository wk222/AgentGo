package bridge

import (
	"encoding/json"
	"math"
	"testing"

	"agentgo/internal/ledger"
)

func usageFixture() []ledger.DayUsage {
	return []ledger.DayUsage{
		{Date: "2026-10-25", Models: []ledger.DailyModelUsage{
			{Model: "large", Calls: 1, PromptTokens: 900, CachedTokens: 600, CompletionTokens: 100, ReasoningTokens: 40, TotalTokens: 1000},
			{Model: "small", Calls: 2, PromptTokens: 100, CompletionTokens: 30, TotalTokens: 130},
		}},
		{Date: "2026-10-26", Models: []ledger.DailyModelUsage{}},
		{Date: "2026-10-27", Models: []ledger.DailyModelUsage{
			{Model: "small", APIKey: "UNKNOWN", Calls: 3, PromptTokens: 300, CachedTokens: 150, CompletionTokens: 60, TotalTokens: 360},
		}},
	}
}

func TestSummarizeUsageFoldsDaysAndModels(t *testing.T) {
	sum := summarizeUsage(usageFixture())
	if sum.Days != 3 || sum.From != "2026-10-25" || sum.To != "2026-10-27" {
		t.Fatalf("window: %+v", sum)
	}
	tot := sum.Totals
	if tot.Calls != 6 || tot.PromptTokens != 1300 || tot.CachedTokens != 750 || tot.CompletionTokens != 190 || tot.ReasoningTokens != 40 || tot.TotalTokens != 1490 {
		t.Fatalf("totals: %+v", tot)
	}
	if want := 750.0 / 1300.0; math.Abs(tot.CacheHitRate-want) > 1e-9 {
		t.Fatalf("cache hit rate %v, want %v", tot.CacheHitRate, want)
	}
	if len(sum.ByModel) != 2 || sum.ByModel[0].Model != "large" || sum.ByModel[1].Model != "small" {
		t.Fatalf("models are not largest first: %+v", sum.ByModel)
	}
	if small := sum.ByModel[1]; small.Calls != 5 || small.PromptTokens != 400 || small.CachedTokens != 150 || small.TotalTokens != 490 {
		t.Fatalf("a model used on two days must be one row: %+v", small)
	}
	if len(sum.ByDay) != 3 || sum.ByDay[0].TotalTokens != 1130 || sum.ByDay[1].TotalTokens != 0 || sum.ByDay[2].Calls != 3 {
		t.Fatalf("days: %+v", sum.ByDay)
	}
	if sum.ByDay[1].CacheHitRate != 0 {
		t.Fatalf("a day with no tokens has no hit rate: %+v", sum.ByDay[1])
	}
}

func TestSummarizeUsageOfNothingIsEmptyNotNull(t *testing.T) {
	sum := summarizeUsage(nil)
	raw, err := json.Marshal(sum)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["by_model"] == nil || got["by_day"] == nil {
		t.Fatalf("lists must be [] so the page can map over them: %s", raw)
	}
	// The row types flatten: the page reads model/date next to the numbers.
	raw, _ = json.Marshal(summarizeUsage(usageFixture()).ByModel[0])
	if string(raw) == "" || !json.Valid(raw) {
		t.Fatal("row does not serialize")
	}
	var row map[string]any
	_ = json.Unmarshal(raw, &row)
	if row["model"] != "large" || row["prompt_tokens"] == nil || row["cache_hit_rate"] == nil {
		t.Fatalf("row is not flat: %s", raw)
	}
}

func TestGetUsageSummaryWithoutARunnerSaysSo(t *testing.T) {
	svc := &AppService{rt: &Runtime{}}
	got := svc.GetUsageSummary(7)
	if got["success"] != false || got["error"] == nil {
		t.Fatalf("expected a refusal, got %v", got)
	}
}
