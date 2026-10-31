package bridge

import (
	"sort"
	"time"

	"agentgo/internal/ledger"
)

// usageTotals is the token accounting of a set of model calls. CacheHitRate is cached / prompt tokens: the
// share of what was sent that the provider served from its prompt cache (0 when nothing was sent).
type usageTotals struct {
	Calls            int     `json:"calls"`
	PromptTokens     int     `json:"prompt_tokens"`
	CachedTokens     int     `json:"cached_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	ReasoningTokens  int     `json:"reasoning_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CacheHitRate     float64 `json:"cache_hit_rate"`
}

func (t *usageTotals) add(m ledger.DailyModelUsage) {
	t.Calls += m.Calls
	t.PromptTokens += m.PromptTokens
	t.CachedTokens += m.CachedTokens
	t.CompletionTokens += m.CompletionTokens
	t.ReasoningTokens += m.ReasoningTokens
	t.TotalTokens += m.TotalTokens
}

func (t *usageTotals) finish() {
	if t.PromptTokens > 0 {
		t.CacheHitRate = float64(t.CachedTokens) / float64(t.PromptTokens)
	}
}

type usageByModel struct {
	Model string `json:"model"`
	usageTotals
}

type usageByDay struct {
	Date string `json:"date"`
	usageTotals
}

// usageSummary is what the usage page shows: the whole window, each model, and each day (oldest first).
type usageSummary struct {
	Days    int            `json:"days"`
	From    string         `json:"from"`
	To      string         `json:"to"`
	Totals  usageTotals    `json:"totals"`
	ByModel []usageByModel `json:"by_model"`
	ByDay   []usageByDay   `json:"by_day"`
}

// summarizeUsage folds the daily rollups into totals, a per-model table (largest first) and a per-day series.
// Rollups are keyed by model and masked API key; the page only cares about the model.
func summarizeUsage(days []ledger.DayUsage) usageSummary {
	out := usageSummary{Days: len(days), ByModel: []usageByModel{}, ByDay: []usageByDay{}}
	models := map[string]*usageByModel{}
	for _, day := range days {
		d := usageByDay{Date: day.Date}
		for _, m := range day.Models {
			d.add(m)
			out.Totals.add(m)
			row := models[m.Model]
			if row == nil {
				row = &usageByModel{Model: m.Model}
				models[m.Model] = row
			}
			row.add(m)
		}
		d.finish()
		out.ByDay = append(out.ByDay, d)
	}
	if len(days) > 0 {
		out.From, out.To = days[0].Date, days[len(days)-1].Date
	}
	out.Totals.finish()
	for _, row := range models {
		row.finish()
		out.ByModel = append(out.ByModel, *row)
	}
	sort.Slice(out.ByModel, func(i, j int) bool {
		if out.ByModel[i].TotalTokens != out.ByModel[j].TotalTokens {
			return out.ByModel[i].TotalTokens > out.ByModel[j].TotalTokens
		}
		return out.ByModel[i].Model < out.ByModel[j].Model
	})
	return out
}

// GetUsageSummary reports the model usage of the last `days` days (1 = today, at most 90): calls, input,
// cached, output and reasoning tokens, by model and by day. The numbers are what the provider reported for
// each call, so a provider that returns no usage shows as zero tokens.
func (s *AppService) GetUsageSummary(days int) map[string]any {
	runner := s.rt.AgentRunner()
	if runner == nil || runner.UsageTracer() == nil {
		return map[string]any{"success": false, "error": "模型用量统计尚未就绪"}
	}
	if days < 1 {
		days = 7
	}
	sum := summarizeUsage(runner.UsageTracer().Range(days, time.Now()))
	return map[string]any{
		"success":  true,
		"days":     sum.Days,
		"from":     sum.From,
		"to":       sum.To,
		"totals":   sum.Totals,
		"by_model": sum.ByModel,
		"by_day":   sum.ByDay,
	}
}
