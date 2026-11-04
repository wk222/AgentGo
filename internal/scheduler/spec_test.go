package scheduler

import (
	"strings"
	"testing"
	"time"
)

var shanghai = mustLoc("Asia/Shanghai")

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

func at(y int, mo time.Month, d, h, mi int, loc *time.Location) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, loc)
}

func mustNext(t *testing.T, spec, tz string, now time.Time) time.Time {
	t.Helper()
	sch, err := ParseSchedule(spec, tz, now)
	if err != nil {
		t.Fatalf("%q: %v", spec, err)
	}
	n, ok := sch.Next(now)
	if !ok {
		t.Fatalf("%q: no next run", spec)
	}
	return n
}

func TestCronExpressions(t *testing.T) {
	// 2026-11-04 is a Wednesday.
	now := at(2026, 11, 4, 10, 15, shanghai)
	cases := []struct {
		spec string
		want time.Time
	}{
		{"cron:30 10 * * *", at(2026, 11, 4, 10, 30, shanghai)},
		{"cron:0 9 * * *", at(2026, 11, 5, 9, 0, shanghai)},       // today's 9:00 already passed
		{"cron:*/20 * * * *", at(2026, 11, 4, 10, 20, shanghai)},  // step over the whole range
		{"cron:0 9 * * 1-5", at(2026, 11, 5, 9, 0, shanghai)},     // weekday range
		{"cron:0 9 * * sat,sun", at(2026, 11, 7, 9, 0, shanghai)}, // names and lists
		{"cron:0 9 * * 7", at(2026, 11, 8, 9, 0, shanghai)},       // 7 is Sunday
		{"cron:0 8-18/5 * * *", at(2026, 11, 4, 13, 0, shanghai)}, // ranged step 8,13,18
		{"cron:0 0 1 * *", at(2026, 12, 1, 0, 0, shanghai)},       // first of the month
		{"cron:0 0 29 feb *", at(2028, 2, 29, 0, 0, shanghai)},    // leap day
		{"cron:0 9 15 * mon", at(2026, 11, 9, 9, 0, shanghai)},    // dom OR dow: next Monday
		{"0 9 * * *", at(2026, 11, 5, 9, 0, shanghai)},            // prefix is optional
		{"@hourly", at(2026, 11, 4, 11, 0, shanghai)},             // macro
		{"cron: 5/30 * * * *", at(2026, 11, 4, 10, 35, shanghai)}, // 5/30 = 5,35
	}
	for _, c := range cases {
		if got := mustNext(t, c.spec, "Asia/Shanghai", now); !got.Equal(c.want) {
			t.Errorf("%s: got %s want %s", c.spec, got.Format(time.RFC3339), c.want.Format(time.RFC3339))
		}
	}
}

func TestCronTimeZone(t *testing.T) {
	// 01:00 UTC is 09:00 in Shanghai: the same instant must give the same answer in either zone's terms.
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	got := mustNext(t, "cron:0 9 * * *", "Asia/Shanghai", now)
	if want := time.Date(2026, 11, 4, 1, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("shanghai: %s", got)
	}
	got = mustNext(t, "cron:0 9 * * *", "America/New_York", now)
	if want := at(2026, 11, 4, 9, 0, mustLoc("America/New_York")); !got.Equal(want) {
		t.Fatalf("new york: %s", got)
	}
	if _, err := ParseSchedule("cron:0 9 * * *", "Mars/Olympus", now); err == nil {
		t.Fatal("an unknown zone must be rejected")
	}
}

func TestCronRejectsBadInput(t *testing.T) {
	for _, spec := range []string{
		"cron:0 9 * *", "cron:61 9 * * *", "cron:0 24 * * *", "cron:0 9 32 * *",
		"cron:0 9 * 13 *", "cron:0 9 * * 8", "cron:*/0 * * * *", "cron:5-1 * * * *", "cron:a b c d e",
	} {
		if _, err := ParseSchedule(spec, "", time.Now()); err == nil {
			t.Errorf("%q should not parse", spec)
		}
	}
	// An impossible date has no run at all, and must not hang.
	sch, err := ParseSchedule("cron:0 0 31 feb *", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sch.Next(time.Now()); ok {
		t.Fatal("31 February cannot occur")
	}
}

func TestNaturalSpecs(t *testing.T) {
	now := at(2026, 11, 4, 10, 15, shanghai) // Wednesday
	next := func(spec string) time.Time { return mustNext(t, spec, "Asia/Shanghai", now) }

	if got := next("每30分钟"); !got.Equal(now.Add(30 * time.Minute)) {
		t.Errorf("每30分钟: %s", got)
	}
	if got := next("every 2h"); !got.Equal(now.Add(2 * time.Hour)) {
		t.Errorf("every 2h: %s", got)
	}
	if got := next("每小时"); !got.Equal(now.Add(time.Hour)) {
		t.Errorf("每小时: %s", got)
	}
	for spec, want := range map[string]time.Time{
		"每天9点":          at(2026, 11, 5, 9, 0, shanghai),
		"每天 9:30":       at(2026, 11, 4, 9, 30, shanghai).AddDate(0, 0, 1),
		"每天18点半":        at(2026, 11, 4, 18, 30, shanghai),
		"daily 08:05":   at(2026, 11, 5, 8, 5, shanghai),
		"每周一 9点":        at(2026, 11, 9, 9, 0, shanghai),
		"每周五 17:45":     at(2026, 11, 6, 17, 45, shanghai),
		"每星期日 10点":      at(2026, 11, 8, 10, 0, shanghai),
		"工作日 8:30":      at(2026, 11, 5, 8, 30, shanghai),
		"weekdays 9:00": at(2026, 11, 5, 9, 0, shanghai),
	} {
		if got := next(spec); !got.Equal(want) {
			t.Errorf("%s: got %s want %s", spec, got.Format(time.RFC3339), want.Format(time.RFC3339))
		}
	}
	// A bare "每天" keeps the old meaning: every 24 hours.
	if got := next("每天"); !got.Equal(now.Add(24 * time.Hour)) {
		t.Errorf("每天: %s", got)
	}
	if _, err := ParseSchedule("每10秒", "", now); err == nil {
		t.Error("seconds are not a valid unit")
	}
	if _, err := ParseSchedule("随便什么时候", "", now); err == nil || !strings.Contains(err.Error(), "每周一") {
		t.Errorf("the error must show what is accepted, got %v", err)
	}
}

func TestOneShotSpecs(t *testing.T) {
	now := at(2026, 11, 4, 10, 15, shanghai)
	if got := mustNext(t, "30分钟后", "Asia/Shanghai", now); !got.Equal(now.Add(30 * time.Minute)) {
		t.Errorf("30分钟后: %s", got)
	}
	if got := mustNext(t, "in 2h", "Asia/Shanghai", now); !got.Equal(now.Add(2 * time.Hour)) {
		t.Errorf("in 2h: %s", got)
	}
	if got := mustNext(t, "at:2026-11-05 09:30", "Asia/Shanghai", now); !got.Equal(at(2026, 11, 5, 9, 30, shanghai)) {
		t.Errorf("at: %s", got)
	}
	if got := mustNext(t, "at:2026-11-05T01:30:00Z", "Asia/Shanghai", now); !got.Equal(at(2026, 11, 5, 9, 30, shanghai)) {
		t.Errorf("rfc3339: %s", got)
	}

	sch, _ := ParseSchedule("30分钟后", "Asia/Shanghai", now)
	canon := sch.Canonical("30分钟后")
	if !strings.HasPrefix(canon, "at:") {
		t.Fatalf("a relative one-shot must be stored as an absolute time, got %q", canon)
	}
	later := now.Add(time.Hour)
	again, err := ParseSchedule(canon, "Asia/Shanghai", later)
	if err != nil {
		t.Fatal(err)
	}
	if again.At.Equal(later.Add(30 * time.Minute)) {
		t.Fatal("reading the job back moved the one-shot")
	}
	if _, ok := again.Next(later); ok {
		t.Fatal("a one-shot in the past has no next run")
	}
}

func TestIntervalHasAMinimum(t *testing.T) {
	if _, err := ParseSchedule("every 0m", "", time.Now()); err == nil {
		t.Fatal("a zero interval must be rejected")
	}
}
