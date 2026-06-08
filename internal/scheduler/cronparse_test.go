package scheduler

import "testing"

func TestParseSimpleCronWeekly(t *testing.T) {
	sec, title, err := ParseScheduleSpec("cron:0 9 * * 1")
	if err != nil {
		t.Fatal(err)
	}
	if sec < 60 {
		t.Fatalf("sec=%d title=%s", sec, title)
	}
}

func TestParseSimpleCronDaily(t *testing.T) {
	sec, _, err := ParseScheduleSpec("cron:30 8 * * *")
	if err != nil || sec < 60 {
		t.Fatalf("sec=%d err=%v", sec, err)
	}
}
