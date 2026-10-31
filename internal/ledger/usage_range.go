package ledger

import (
	"sort"
	"time"
)

// MaxUsageDays bounds how far back Range looks: one rollup file per day is read.
const MaxUsageDays = 90

// DayUsage is one calendar day of the usage rollup, models sorted by tokens (largest first).
type DayUsage struct {
	Date   string            `json:"date"`
	Models []DailyModelUsage `json:"models"`
}

// Range returns the rollups of the last `days` calendar days ending on `now`'s date, oldest first. Days
// without any usage are still listed (with no models), so the caller can draw a continuous axis. A day whose
// file cannot be read or parsed is listed empty rather than failing the whole range.
func (t *ModelUsageTracer) Range(days int, now time.Time) []DayUsage {
	if days < 1 {
		days = 1
	}
	if days > MaxUsageDays {
		days = MaxUsageDays
	}
	_ = t.Flush()
	out := make([]DayUsage, 0, days)
	for i := days - 1; i >= 0; i-- {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		day := DayUsage{Date: date, Models: []DailyModelUsage{}}
		if rolled, err := t.readDay(date); err == nil {
			for _, m := range rolled {
				if m != nil {
					day.Models = append(day.Models, *m)
				}
			}
		}
		sort.Slice(day.Models, func(a, b int) bool {
			if day.Models[a].TotalTokens != day.Models[b].TotalTokens {
				return day.Models[a].TotalTokens > day.Models[b].TotalTokens
			}
			return day.Models[a].Model < day.Models[b].Model
		})
		out = append(out, day)
	}
	return out
}
