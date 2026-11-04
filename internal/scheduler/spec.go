package scheduler

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	// A Windows machine often has no zoneinfo database, and a job's time zone must resolve there too.
	_ "time/tzdata"
)

// Kind is how a job repeats.
type Kind string

const (
	KindInterval Kind = "interval" // every N minutes/hours/days
	KindCron     Kind = "cron"     // a cron expression on a wall clock
	KindOnce     Kind = "once"     // a single moment, then the job switches itself off
)

// MinInterval keeps a fixed-interval job from turning into a busy loop.
const MinInterval = time.Minute

// Schedule is a parsed job spec. Use ParseSchedule; the zero value never fires.
type Schedule struct {
	Kind  Kind
	Every time.Duration // KindInterval
	At    time.Time     // KindOnce
	Loc   *time.Location
	cron  *cronExpr
}

// ParseSchedule understands
//
//	cron:0 9 * * 1-5        a cron expression (the "cron:" prefix is optional for 5 fields and @macros)
//	at:2026-11-05 09:30     one moment, wall-clock in tz (also RFC 3339)
//	每30分钟 / every 2h      a fixed interval (at least a minute)
//	每天9点 / 每天9:30 / daily 09:30
//	每周一 9点 / 工作日 8:30 / weekdays 08:30
//	30分钟后 / in 2h         once, that long from `now`
//
// tz is an IANA zone name such as Asia/Shanghai; empty means the machine's local zone.
func ParseSchedule(spec, tz string, now time.Time) (Schedule, error) {
	loc := time.Local
	if tz = strings.TrimSpace(tz); tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return Schedule{}, fmt.Errorf("未知时区 %q（用 Asia/Shanghai 这样的 IANA 名称）", tz)
		}
		loc = l
	}
	s := strings.TrimSpace(spec)
	low := strings.ToLower(s)
	switch {
	case s == "":
		return Schedule{}, fmt.Errorf("定时描述不能为空")
	case strings.HasPrefix(low, "cron:"):
		return cronSchedule(strings.TrimSpace(s[5:]), loc)
	case strings.HasPrefix(low, "at:"):
		return onceSchedule(strings.TrimSpace(s[3:]), loc)
	case strings.HasPrefix(low, "@"):
		return cronSchedule(s, loc)
	}
	if f := strings.Fields(s); len(f) == 5 {
		if sch, err := cronSchedule(s, loc); err == nil {
			return sch, nil
		}
	}
	return naturalSchedule(low, loc, now)
}

func cronSchedule(expr string, loc *time.Location) (Schedule, error) {
	c, err := parseCron(expr)
	if err != nil {
		return Schedule{}, err
	}
	return Schedule{Kind: KindCron, cron: c, Loc: loc}, nil
}

var onceLayouts = []string{
	time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04",
}

func onceSchedule(v string, loc *time.Location) (Schedule, error) {
	for _, layout := range onceLayouts {
		if t, err := time.ParseInLocation(layout, v, loc); err == nil {
			return Schedule{Kind: KindOnce, At: t, Loc: loc}, nil
		}
	}
	return Schedule{}, fmt.Errorf("无法解析时间 %q，请用 at:2026-11-05 09:30 或 RFC3339 格式", v)
}

var (
	reAfter  = regexp.MustCompile(`^(\d+)\s*(分钟|分|小时|天|min|minutes?|m|hours?|h|days?|d)\s*(?:后|以后|之后)$`)
	reIn     = regexp.MustCompile(`^in\s+(\d+)\s*(minutes?|min|m|hours?|h|days?|d)$`)
	reEvery  = regexp.MustCompile(`^(?:每|every)\s*(\d*)\s*(分钟|分|小时|天|min|minutes?|m|hours?|h|days?|d)$`)
	reClock  = regexp.MustCompile(`(\d{1,2})\s*(?::|：|点|时|h)\s*(半|\d{1,2})?`)
	reWeekly = regexp.MustCompile(`(?:每周|每星期|每礼拜|weekly|every)\s*(?:周|星期|礼拜)?\s*(一|二|三|四|五|六|日|天|mon|tue|wed|thu|fri|sat|sun)`)
)

func unitDuration(unit string) time.Duration {
	switch {
	case strings.HasPrefix(unit, "分"), strings.HasPrefix(unit, "min"), unit == "m":
		return time.Minute
	case strings.HasPrefix(unit, "小时"), strings.HasPrefix(unit, "hour"), unit == "h":
		return time.Hour
	}
	return 24 * time.Hour
}

var weekdayNum = map[string]int{
	"日": 0, "天": 0, "sun": 0, "一": 1, "mon": 1, "二": 2, "tue": 2, "三": 3, "wed": 3,
	"四": 4, "thu": 4, "五": 5, "fri": 5, "六": 6, "sat": 6,
}

// clock finds a time of day ("9点", "9:30", "9点半", "09:30") in s.
func clock(s string) (h, m int, ok bool) {
	g := reClock.FindStringSubmatch(s)
	if g == nil {
		return 0, 0, false
	}
	h, _ = strconv.Atoi(g[1])
	switch g[2] {
	case "":
	case "半":
		m = 30
	default:
		m, _ = strconv.Atoi(g[2])
	}
	if h > 23 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

func naturalSchedule(s string, loc *time.Location, now time.Time) (Schedule, error) {
	if g := reAfter.FindStringSubmatch(s); g != nil {
		return afterSchedule(g[1], g[2], loc, now)
	}
	if g := reIn.FindStringSubmatch(s); g != nil {
		return afterSchedule(g[1], g[2], loc, now)
	}
	if g := reEvery.FindStringSubmatch(s); g != nil {
		n := int64(1)
		if g[1] != "" {
			n, _ = strconv.ParseInt(g[1], 10, 64)
		}
		d := time.Duration(n) * unitDuration(g[2])
		if d < MinInterval {
			return Schedule{}, fmt.Errorf("间隔至少 1 分钟")
		}
		return Schedule{Kind: KindInterval, Every: d, Loc: loc}, nil
	}
	if h, m, ok := clock(s); ok {
		switch {
		case strings.Contains(s, "工作日") || strings.Contains(s, "weekday"):
			return cronSchedule(fmt.Sprintf("%d %d * * 1-5", m, h), loc)
		case reWeekly.MatchString(s):
			w := reWeekly.FindStringSubmatch(s)[1]
			return cronSchedule(fmt.Sprintf("%d %d * * %d", m, h, weekdayNum[w]), loc)
		case strings.Contains(s, "每天") || strings.Contains(s, "每日") || strings.Contains(s, "daily") || strings.Contains(s, "every day"):
			return cronSchedule(fmt.Sprintf("%d %d * * *", m, h), loc)
		}
	}
	switch s {
	case "每天", "每日", "daily":
		return Schedule{Kind: KindInterval, Every: 24 * time.Hour, Loc: loc}, nil
	case "每小时", "hourly":
		return Schedule{Kind: KindInterval, Every: time.Hour, Loc: loc}, nil
	}
	return Schedule{}, fmt.Errorf("无法解析定时描述 %q。可用：「每30分钟」「每天9点」「每周一 9:30」「工作日 8:30」「30分钟后」「cron:0 9 * * 1-5」「at:2026-11-05 09:30」", s)
}

func afterSchedule(num, unit string, loc *time.Location, now time.Time) (Schedule, error) {
	n, _ := strconv.ParseInt(num, 10, 64)
	if n <= 0 {
		return Schedule{}, fmt.Errorf("时间必须大于 0")
	}
	return Schedule{Kind: KindOnce, At: now.Add(time.Duration(n) * unitDuration(unit)), Loc: loc}, nil
}

// Next is the first run strictly after `after`; ok is false when the schedule has no more runs.
func (s Schedule) Next(after time.Time) (time.Time, bool) {
	switch s.Kind {
	case KindInterval:
		return after.Add(s.Every), true
	case KindOnce:
		return s.At, s.At.After(after)
	case KindCron:
		return s.cron.next(after, s.Loc)
	}
	return time.Time{}, false
}

// Canonical is the spec to store. A relative one-shot ("30分钟后") is pinned to its absolute
// time so reading the job back later does not move it.
func (s Schedule) Canonical(spec string) string {
	if s.Kind == KindOnce {
		return "at:" + s.At.In(s.Loc).Format("2006-01-02T15:04:05Z07:00")
	}
	return strings.TrimSpace(spec)
}
