package ledger

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRangeListsEveryDayOldestFirstAndKeepsGapsEmpty(t *testing.T) {
	dir := t.TempDir()
	tracer := NewModelUsageTracer(dir, time.Hour)
	defer tracer.Close()

	now := time.Date(2026, 10, 27, 15, 0, 0, 0, time.Local)
	// Two days ago: two models. Yesterday: nothing.
	old := `{
  "small|UNKNOWN": {"model": "small", "api_key": "UNKNOWN", "calls": 2, "prompt_tokens": 100, "total_tokens": 130, "completion_tokens": 30},
  "large|UNKNOWN": {"model": "large", "api_key": "UNKNOWN", "calls": 1, "prompt_tokens": 900, "total_tokens": 1000, "completion_tokens": 100}
}`
	if err := os.WriteFile(filepath.Join(dir, "2026-10-25.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	// Usage recorded now is filed under the real clock's date and is still only in memory.
	tracer.Record("small", "", 50, 10, 5, 0, 0)

	days := tracer.Range(3, now)
	if len(days) != 3 || days[0].Date != "2026-10-25" || days[1].Date != "2026-10-26" || days[2].Date != "2026-10-27" {
		t.Fatalf("range: %+v", days)
	}
	if got := days[0].Models; len(got) != 2 || got[0].Model != "large" || got[1].Model != "small" {
		t.Fatalf("models are not sorted by tokens: %+v", got)
	}
	if len(days[1].Models) != 0 {
		t.Fatalf("a day without usage must list no models: %+v", days[1].Models)
	}
	if days[1].Models == nil {
		t.Fatalf("an empty day must serialize as [] not null")
	}

	// Live usage is flushed before the range is read: ask for the window that ends at the real clock's today.
	real := tracer.Range(1, time.Now())
	if len(real) != 1 || len(real[0].Models) != 1 || real[0].Models[0].PromptTokens != 50 || real[0].Models[0].CachedTokens != 10 {
		t.Fatalf("today's pending usage is missing: %+v", real)
	}
}

func TestRangeClampsDaysAndSurvivesABrokenFile(t *testing.T) {
	dir := t.TempDir()
	tracer := NewModelUsageTracer(dir, time.Hour)
	defer tracer.Close()
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.Local)
	if err := os.WriteFile(filepath.Join(dir, "2026-10-27.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tracer.Range(0, now); len(got) != 1 || len(got[0].Models) != 0 {
		t.Fatalf("a broken file must read as an empty day: %+v", got)
	}
	if got := tracer.Range(10_000, now); len(got) != MaxUsageDays {
		t.Fatalf("range is not bounded: %d days", len(got))
	}
}
