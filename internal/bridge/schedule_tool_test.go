package bridge

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"agentgo/internal/scheduler"
	"agentgo/internal/taskhub"
	"agentgo/internal/tools"

	_ "modernc.org/sqlite"
)

func scheduleRuntime(t *testing.T) *Runtime {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	st, err := scheduler.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	hub, err := taskhub.New(db)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan taskhub.Task, 4)
	hub.SetRunner(func(_ context.Context, tk taskhub.Task, _ func(taskhub.Event)) error {
		started <- tk
		return nil
	})
	return &Runtime{sched: st, taskHub: hub}
}

func TestScheduleHandlerOwnership(t *testing.T) {
	rt := scheduleRuntime(t)
	ctx := context.Background()
	res, err := rt.CreateSchedule(ctx, tools.ScheduleRequest{Spec: "每30分钟", Prompt: "check the build", SessionID: "A"})
	if err != nil {
		t.Fatal(err)
	}
	job := res.(scheduler.Job)
	if job.SessionID != "A" || job.Origin != "A" || job.Kind != "interval" {
		t.Fatalf("a job continues its own conversation by default: %+v", job)
	}

	fresh, err := rt.CreateSchedule(ctx, tools.ScheduleRequest{Spec: "cron:0 9 * * 1-5", TZ: "Asia/Shanghai", Prompt: "report", SessionID: "A", NewSession: true})
	if err != nil {
		t.Fatal(err)
	}
	if j := fresh.(scheduler.Job); j.SessionID != "" || j.Origin != "A" || j.TZ != "Asia/Shanghai" {
		t.Fatalf("new-conversation job: %+v", j)
	}

	listed, _ := rt.ListSchedules(ctx, "A")
	if got := len(listed.([]scheduler.Job)); got != 2 {
		t.Fatalf("A sees %d jobs, want 2", got)
	}
	if other, _ := rt.ListSchedules(ctx, "B"); len(other.([]scheduler.Job)) != 0 {
		t.Fatal("B must not see A's jobs")
	}
	if err := rt.DeleteSchedule(ctx, "B", job.ID); err == nil || !strings.Contains(err.Error(), "another conversation") {
		t.Fatalf("B deleted A's job: %v", err)
	}
	if err := rt.SetScheduleEnabled(ctx, "A", job.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := rt.sched.Get(job.ID); got.Enabled {
		t.Fatal("pause did not stick")
	}
	if err := rt.DeleteSchedule(ctx, "A", fresh.(scheduler.Job).ID); err != nil {
		t.Fatalf("A must manage a job that opens new conversations: %v", err)
	}
	if _, err := rt.CreateSchedule(ctx, tools.ScheduleRequest{Spec: "每30分钟", Prompt: "x"}); err == nil {
		t.Fatal("a job needs an owning conversation")
	}
	if _, err := rt.CreateSchedule(ctx, tools.ScheduleRequest{Spec: "whenever", Prompt: "x", SessionID: "A"}); err == nil {
		t.Fatal("a spec that does not parse must be refused")
	}
}

func TestScheduleHandlerLimits(t *testing.T) {
	rt := scheduleRuntime(t)
	ctx := context.Background()
	for i := 0; i < maxSchedulesPerSession; i++ {
		if _, err := rt.CreateSchedule(ctx, tools.ScheduleRequest{Spec: "每30分钟", Prompt: "x", SessionID: "A"}); err != nil {
			t.Fatalf("job %d: %v", i, err)
		}
	}
	if _, err := rt.CreateSchedule(ctx, tools.ScheduleRequest{Spec: "每30分钟", Prompt: "x", SessionID: "A"}); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("the per-conversation cap did not hold: %v", err)
	}
	// Another conversation is unaffected until the global cap.
	if _, err := rt.CreateSchedule(ctx, tools.ScheduleRequest{Spec: "每30分钟", Prompt: "x", SessionID: "B"}); err != nil {
		t.Fatal(err)
	}
}

func TestRunScheduleNowStartsATurnWithoutMovingTheSchedule(t *testing.T) {
	rt := scheduleRuntime(t)
	ctx := context.Background()
	res, _ := rt.CreateSchedule(ctx, tools.ScheduleRequest{Spec: "cron:0 9 * * *", TZ: "Asia/Shanghai", Prompt: "daily", SessionID: "A"})
	job := res.(scheduler.Job)
	out, err := rt.RunScheduleNow(ctx, "A", job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.(map[string]any)["task_id"] == "" {
		t.Fatal("no task started")
	}
	got, _ := rt.sched.Get(job.ID)
	if got.FireCount != 1 || got.NextRunAt != job.NextRunAt {
		t.Fatalf("a manual run must count but keep the cadence: before %d after %d fires=%d", job.NextRunAt, got.NextRunAt, got.FireCount)
	}
	if _, err := rt.RunScheduleNow(ctx, "B", job.ID); err == nil {
		t.Fatal("B ran A's job")
	}
}

func TestScheduledInputExplainsACatchUp(t *testing.T) {
	on := time.Date(2026, 11, 4, 9, 0, 0, 0, time.Local)
	if got := scheduledInput(scheduler.Job{Prompt: "p"}); got != "p" {
		t.Fatalf("an on-time run keeps the prompt as is: %q", got)
	}
	got := scheduledInput(scheduler.Job{Prompt: "p", Late: true, ScheduledFor: on.Unix()})
	if !strings.HasPrefix(got, "p\n\n") || !strings.Contains(got, "2026-11-04 09:00") || !strings.Contains(got, "补跑") {
		t.Fatalf("catch-up note: %q", got)
	}
}

func TestScheduledTitle(t *testing.T) {
	for in, want := range map[string]string{
		"汇总日志\n第二行":             "汇总日志",
		"":                      "定时任务",
		strings.Repeat("字", 40): strings.Repeat("字", 24) + "…",
	} {
		if got := scheduledTitle(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
