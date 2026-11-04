package scheduler

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1) // an in-memory database exists per connection
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// newStore returns a store whose clock the test moves.
func newStore(t *testing.T, start time.Time) (*Store, *time.Time) {
	t.Helper()
	s, err := NewStore(openDB(t))
	if err != nil {
		t.Fatal(err)
	}
	clock := start
	s.now = func() time.Time { return clock }
	return s, &clock
}

func TestCronJobKeepsItsWallClockCadence(t *testing.T) {
	// The first release froze "seconds until the first run" and reused it as the period, so a job
	// made at 20:00 for 09:00 ran every 13 hours. Every run must land on 09:00.
	s, clock := newStore(t, at(2026, 11, 4, 20, 0, shanghai))
	j, err := s.CreateWith(CreateOptions{Spec: "cron:0 9 * * *", TZ: "Asia/Shanghai", Prompt: "daily report"})
	if err != nil {
		t.Fatal(err)
	}
	for day := 5; day <= 8; day++ {
		want := at(2026, 11, day, 9, 0, shanghai)
		got, _ := s.Get(j.ID)
		if got.NextRunAt != want.Unix() {
			t.Fatalf("run %d is due %s, want %s", day-4, time.Unix(got.NextRunAt, 0).In(shanghai), want)
		}
		*clock = want.Add(5 * time.Second)
		due, _ := s.Due(clock.Unix())
		if len(due) != 1 {
			t.Fatalf("day %d: %d jobs due", day, len(due))
		}
		claimed, ok, err := s.Claim(due[0])
		if err != nil || !ok {
			t.Fatalf("claim: ok=%v err=%v", ok, err)
		}
		if claimed.Late {
			t.Fatal("a run fired 5 s after its time is not late")
		}
	}
}

func TestClaimIsExclusive(t *testing.T) {
	s, clock := newStore(t, at(2026, 11, 4, 9, 0, shanghai))
	j, err := s.CreateWith(CreateOptions{Spec: "每30分钟", Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(31 * time.Minute)
	due, _ := s.Due(clock.Unix())
	if len(due) != 1 {
		t.Fatalf("due=%d", len(due))
	}
	wins := 0
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok, _ := s.Claim(due[0]); ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d callers claimed the same run", wins)
	}
	got, _ := s.Get(j.ID)
	if got.FireCount != 1 || got.NextRunAt != clock.Add(30*time.Minute).Unix() {
		t.Fatalf("fire_count=%d next=%d", got.FireCount, got.NextRunAt)
	}
}

func TestMissedRunFiresOnceAndIsMarkedLate(t *testing.T) {
	s, clock := newStore(t, at(2026, 11, 4, 9, 0, shanghai))
	j, _ := s.CreateWith(CreateOptions{Spec: "每30分钟", Prompt: "x"})
	*clock = clock.Add(10 * time.Hour) // the laptop slept through twenty runs
	due, _ := s.Due(clock.Unix())
	claimed, ok, _ := s.Claim(due[0])
	if !ok || !claimed.Late {
		t.Fatalf("ok=%v late=%v", ok, claimed.Late)
	}
	if claimed.ScheduledFor != j.NextRunAt {
		t.Fatal("the run must say when it was originally due")
	}
	if again, _ := s.Due(clock.Unix()); len(again) != 0 {
		t.Fatal("the backlog must not be replayed")
	}
}

func TestOneShotSwitchesItselfOff(t *testing.T) {
	s, clock := newStore(t, at(2026, 11, 4, 9, 0, shanghai))
	j, err := s.CreateWith(CreateOptions{Spec: "30分钟后", Prompt: "remind me"})
	if err != nil {
		t.Fatal(err)
	}
	if j.Kind != "once" || j.NextRunAt != clock.Add(30*time.Minute).Unix() {
		t.Fatalf("job: %+v", j)
	}
	*clock = clock.Add(31 * time.Minute)
	due, _ := s.Due(clock.Unix())
	claimed, ok, _ := s.Claim(due[0])
	if !ok || claimed.Enabled {
		t.Fatalf("ok=%v enabled=%v", ok, claimed.Enabled)
	}
	if got, _ := s.Get(j.ID); got.Enabled {
		t.Fatal("a spent one-shot must stay disabled")
	}
	if err := s.SetEnabled(j.ID, true); err == nil {
		t.Fatal("a one-shot in the past cannot be re-enabled")
	}
}

func TestCreateRejectsPastAndBadSpecs(t *testing.T) {
	s, _ := newStore(t, at(2026, 11, 4, 9, 0, shanghai))
	for _, spec := range []string{"at:2020-01-01 09:00", "nonsense", "cron:1 2 3", ""} {
		if _, err := s.CreateWith(CreateOptions{Spec: spec, TZ: "Asia/Shanghai", Prompt: "x"}); err == nil {
			t.Errorf("%q should be rejected", spec)
		}
	}
}

func TestReEnableSkipsTheBacklog(t *testing.T) {
	s, clock := newStore(t, at(2026, 11, 4, 9, 0, shanghai))
	j, _ := s.CreateWith(CreateOptions{Spec: "cron:0 9 * * *", TZ: "Asia/Shanghai", Prompt: "x"})
	_ = s.SetEnabled(j.ID, false)
	*clock = at(2026, 11, 20, 12, 0, shanghai)
	if err := s.SetEnabled(j.ID, true); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(j.ID)
	if want := at(2026, 11, 21, 9, 0, shanghai).Unix(); got.NextRunAt != want {
		t.Fatalf("next=%s want %s", time.Unix(got.NextRunAt, 0).In(shanghai), time.Unix(want, 0).In(shanghai))
	}
}

func TestMarkRanKeepsCronCadence(t *testing.T) {
	s, clock := newStore(t, at(2026, 11, 4, 8, 0, shanghai))
	j, _ := s.CreateWith(CreateOptions{Spec: "cron:0 9 * * *", TZ: "Asia/Shanghai", Prompt: "x"})
	*clock = clock.Add(10 * time.Minute) // the user pressed "run now"
	if err := s.MarkRan(j.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(j.ID)
	if got.NextRunAt != at(2026, 11, 4, 9, 0, shanghai).Unix() || got.FireCount != 1 {
		t.Fatalf("a manual run must not move the schedule: %+v", got)
	}
}

func TestOldDatabaseIsMigrated(t *testing.T) {
	db := openDB(t)
	if _, err := db.Exec(`CREATE TABLE scheduled_jobs (id TEXT PRIMARY KEY, title TEXT, spec TEXT NOT NULL, prompt TEXT NOT NULL,
		session_id TEXT, next_run_at INTEGER NOT NULL, interval_sec INTEGER NOT NULL DEFAULT 0, enabled INTEGER NOT NULL DEFAULT 1,
		last_run_at INTEGER DEFAULT 0, created_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO scheduled_jobs (id, title, spec, prompt, session_id, next_run_at, interval_sec, created_at)
		VALUES ('old','每30分钟','每30分钟','p','s1',100,1800,50)`); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(db); err != nil {
		t.Fatalf("opening twice must be harmless: %v", err)
	}
	got, err := s.Get("old")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "interval" || got.SessionID != "s1" || got.FireCount != 0 || !got.Enabled {
		t.Fatalf("migrated job: %+v", got)
	}
}

func TestRunnerFiresDueJobsAtOnce(t *testing.T) {
	s, clock := newStore(t, at(2026, 11, 4, 9, 0, shanghai))
	_, _ = s.CreateWith(CreateOptions{Spec: "每30分钟", Prompt: "tick", SessionID: "s1"})
	*clock = clock.Add(2 * time.Hour) // an app that was closed over the due time

	fired := make(chan Job, 4)
	r := NewRunner(s, func(_ context.Context, j Job) { fired <- j })
	r.tick = time.Hour
	r.Start()
	defer r.Stop()
	select {
	case j := <-fired:
		if j.Prompt != "tick" || !j.Late || j.SessionID != "s1" {
			t.Fatalf("fired %+v", j)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a job that came due while the app was closed must fire on start, not after the first tick")
	}
}
