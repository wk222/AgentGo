package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseScheduleSpec returns interval seconds for natural language or simple cron.
// Cron subset: "cron:MIN HOUR * * DOW" (5 fields; day/month must be *; DOW 0-6, Sun=0, or *).
func ParseScheduleSpec(spec string) (intervalSec int64, title string, err error) {
	spec = strings.TrimSpace(spec)
	title = spec
	if strings.HasPrefix(strings.ToLower(spec), "cron:") {
		return parseSimpleCron(strings.TrimSpace(spec[5:]))
	}
	return ParseNaturalSpec(spec)
}

func parseSimpleCron(expr string) (int64, string, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return 0, expr, fmt.Errorf("cron 需要 5 段，例如 cron:0 9 * * *")
	}
	min, err1 := parseCronField(parts[0], 0, 59)
	hour, err2 := parseCronField(parts[1], 0, 23)
	if err1 != nil || err2 != nil {
		return 0, expr, fmt.Errorf("cron 分/时解析失败")
	}
	if parts[2] != "*" || parts[3] != "*" {
		return 0, expr, fmt.Errorf("当前日/月字段仅支持 *（示例 cron:30 9 * * 1 表示每周一 9:30）")
	}
	var dow *int
	if parts[4] != "*" {
		w, err := parseCronField(parts[4], 0, 6)
		if err != nil {
			return 0, expr, fmt.Errorf("cron 星期字段: %w", err)
		}
		dow = &w
	}
	now := time.Now()
	next := nextCronOccurrence(now, hour, min, dow)
	sec := int64(next.Sub(now).Seconds())
	if sec < 60 {
		sec = 60
	}
	return sec, "cron:" + expr, nil
}

// nextCronOccurrence finds the next local time at hour:min, optionally matching cron DOW (0=Sunday).
func nextCronOccurrence(now time.Time, hour, min int, dow *int) time.Time {
	loc := now.Location()
	for day := 0; day < 14; day++ {
		base := now.AddDate(0, 0, day)
		candidate := time.Date(base.Year(), base.Month(), base.Day(), hour, min, 0, 0, loc)
		if !candidate.After(now) {
			continue
		}
		if dow != nil && int(candidate.Weekday()) != *dow {
			continue
		}
		return candidate
	}
	return now.Add(24 * time.Hour)
}

func parseCronField(field string, min, max int) (int, error) {
	if field == "*" {
		return min, nil
	}
	v, err := strconv.Atoi(field)
	if err != nil || v < min || v > max {
		return 0, fmt.Errorf("invalid cron field %q", field)
	}
	return v, nil
}
