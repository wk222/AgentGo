package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cronExpr is a standard 5-field cron expression: minute hour day-of-month month day-of-week.
// Every field accepts "*", "a", "a-b", lists "a,b-c", and steps "*/n" or "a-b/n".
// Months and weekdays also accept English names (jan, mon, …). Day-of-week 7 means Sunday.
// As in Vixie cron, when both day fields are restricted a day matches if EITHER does.
type cronExpr struct {
	min, hour, dom, mon, dow uint64
	domStar, dowStar         bool
}

var cronMacros = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

var (
	monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
	dayNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}
)

func parseCron(expr string) (*cronExpr, error) {
	expr = strings.TrimSpace(expr)
	if m, ok := cronMacros[strings.ToLower(expr)]; ok {
		expr = m
	}
	f := strings.Fields(expr)
	if len(f) != 5 {
		return nil, fmt.Errorf("cron 需要 5 段（分 时 日 月 周），例如 0 9 * * 1-5，收到 %d 段", len(f))
	}
	var c cronExpr
	var err error
	if c.min, _, err = parseCronField(f[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("cron 分钟段: %w", err)
	}
	if c.hour, _, err = parseCronField(f[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("cron 小时段: %w", err)
	}
	if c.dom, c.domStar, err = parseCronField(f[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("cron 日期段: %w", err)
	}
	if c.mon, _, err = parseCronField(f[3], 1, 12, monthNames); err != nil {
		return nil, fmt.Errorf("cron 月份段: %w", err)
	}
	if c.dow, c.dowStar, err = parseCronField(f[4], 0, 7, dayNames); err != nil {
		return nil, fmt.Errorf("cron 星期段: %w", err)
	}
	if c.dow&(1<<7) != 0 { // 7 is Sunday too
		c.dow = c.dow&^(1<<7) | 1
	}
	return &c, nil
}

// parseCronField returns the bit set of the values the field selects and whether the field was
// a bare wildcard ("*" or "?").
func parseCronField(s string, lo, hi int, names map[string]int) (mask uint64, star bool, err error) {
	if s == "*" || s == "?" {
		for v := lo; v <= hi; v++ {
			mask |= 1 << uint(v)
		}
		return mask, true, nil
	}
	for _, part := range strings.Split(s, ",") {
		step := 1
		rng := part
		if i := strings.IndexByte(part, '/'); i >= 0 {
			rng = part[:i]
			if step, err = strconv.Atoi(part[i+1:]); err != nil || step < 1 {
				return 0, false, fmt.Errorf("步长 %q 无效", part[i+1:])
			}
		}
		from, to := lo, hi
		switch {
		case rng == "*" || rng == "?":
		case strings.Contains(rng, "-"):
			a, b, _ := strings.Cut(rng, "-")
			if from, err = cronValue(a, lo, hi, names); err != nil {
				return 0, false, err
			}
			if to, err = cronValue(b, lo, hi, names); err != nil {
				return 0, false, err
			}
			if from > to {
				return 0, false, fmt.Errorf("范围 %q 起点大于终点", rng)
			}
		default:
			if from, err = cronValue(rng, lo, hi, names); err != nil {
				return 0, false, err
			}
			if strings.Contains(part, "/") {
				to = hi // "5/15" means 5 through the end, every 15
			} else {
				to = from
			}
		}
		for v := from; v <= to; v += step {
			mask |= 1 << uint(v)
		}
	}
	return mask, false, nil
}

func cronValue(s string, lo, hi int, names map[string]int) (int, error) {
	if v, ok := names[strings.ToLower(s)]; ok {
		return v, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < lo || v > hi {
		return 0, fmt.Errorf("值 %q 超出 %d-%d", s, lo, hi)
	}
	return v, nil
}

func (c *cronExpr) dayMatches(d time.Time) bool {
	if c.mon&(1<<uint(d.Month())) == 0 {
		return false
	}
	domOK := c.dom&(1<<uint(d.Day())) != 0
	dowOK := c.dow&(1<<uint(d.Weekday())) != 0
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return dowOK
	case c.dowStar:
		return domOK
	}
	return domOK || dowOK
}

// next is the first matching minute strictly after `after`, evaluated on loc's wall clock. It
// gives up after five years, which only an impossible date such as "0 0 31 2 *" can reach.
func (c *cronExpr) next(after time.Time, loc *time.Location) (time.Time, bool) {
	a := after.In(loc)
	start := time.Date(a.Year(), a.Month(), a.Day(), a.Hour(), a.Minute()+1, 0, 0, loc)
	for d := 0; d < 366*5+2; d++ {
		day := time.Date(start.Year(), start.Month(), start.Day()+d, 0, 0, 0, 0, loc)
		if !c.dayMatches(day) {
			continue
		}
		for h := 0; h < 24; h++ {
			if c.hour&(1<<uint(h)) == 0 {
				continue
			}
			for m := 0; m < 60; m++ {
				if c.min&(1<<uint(m)) == 0 {
					continue
				}
				cand := time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, loc)
				if cand.After(after) && !cand.Before(start) {
					return cand, true
				}
			}
		}
	}
	return time.Time{}, false
}
