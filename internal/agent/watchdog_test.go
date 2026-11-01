package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type wdFixture struct {
	w       *Watchdog
	rc      *RunControl
	mu      sync.Mutex
	actions []WatchdogAction
	now     time.Time
}

func newWDFixture() *wdFixture {
	rc := NewRunControl()
	f := &wdFixture{rc: rc, now: time.Now()}
	f.w = newWatchdog(&Runner{runControl: rc})
	f.w.now = func() time.Time { return f.now }
	f.w.notify = func(a WatchdogAction) {
		f.mu.Lock()
		f.actions = append(f.actions, a)
		f.mu.Unlock()
	}
	return f
}

// start registers a run that has been quiet for quiet; the returned context
// ends when the watchdog stops the run.
func (f *wdFixture) start(session string, quiet time.Duration, inTool bool) (*runActivity, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	act := newRunActivity()
	act.last.Store(f.now.Add(-quiet).UnixNano())
	if inTool {
		act.inflight.Store(1)
	}
	f.rc.SetCtxCancel(session, cancel)
	f.rc.track(session, act)
	return act, ctx
}

func (f *wdFixture) got() []WatchdogAction {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]WatchdogAction(nil), f.actions...)
}

func TestWatchdogLeavesAnActiveRunAlone(t *testing.T) {
	f := newWDFixture()
	_, ctx := f.start("s", time.Minute, false)
	f.w.Patrol()
	if len(f.got()) != 0 || ctx.Err() != nil {
		t.Fatal("a run that was active a minute ago was stopped")
	}
}

func TestWatchdogStopsAHungModelAndAsksToContinue(t *testing.T) {
	f := newWDFixture()
	_, ctx := f.start("s", watchdogStall+time.Second, false)
	f.w.Patrol()
	a := f.got()
	if len(a) != 1 || a[0].Escalated || a[0].SessionID != "s" || a[0].InTool {
		t.Fatalf("want one recoverable stall, got %+v", a)
	}
	if ctx.Err() == nil {
		t.Fatal("the stalled run was not stopped")
	}
	if !strings.Contains(a[0].Text, "继续") {
		t.Fatalf("the wake text should ask to continue: %q", a[0].Text)
	}
}

func TestWatchdogGivesALongToolMoreTime(t *testing.T) {
	f := newWDFixture()
	_, ctx := f.start("s", 20*time.Minute, true) // past the model limit, inside the tool limit
	f.w.Patrol()
	if len(f.got()) != 0 || ctx.Err() != nil {
		t.Fatal("a run waiting on a tool was treated as a hung model")
	}
	f.now = f.now.Add(30 * time.Minute) // now past the tool ceiling too
	f.w.Patrol()
	a := f.got()
	if len(a) != 1 || !a[0].InTool {
		t.Fatalf("a tool that never returned should be caught: %+v", a)
	}
}

func TestWatchdogReportsAStoppedRunOnlyOnce(t *testing.T) {
	f := newWDFixture()
	f.start("s", watchdogStall+time.Second, false)
	f.w.Patrol()
	f.w.Patrol() // the run has not unwound yet
	if n := len(f.got()); n != 1 {
		t.Fatalf("one stall produced %d actions", n)
	}
}

func TestWatchdogEscalatesAfterRepeatedStalls(t *testing.T) {
	f := newWDFixture()
	for i := 0; i < watchdogMaxRecoveries; i++ {
		f.start("s", watchdogStall+time.Second, false)
		f.w.Patrol()
		f.rc.Clear("s") // the run ended; the next turn stalls again
		f.w.Patrol()
		f.now = f.now.Add(time.Minute)
	}
	f.start("s", watchdogStall+time.Second, false)
	f.w.Patrol()
	a := f.got()
	if len(a) != watchdogMaxRecoveries+1 {
		t.Fatalf("want %d actions, got %d: %+v", watchdogMaxRecoveries+1, len(a), a)
	}
	for _, x := range a[:watchdogMaxRecoveries] {
		if x.Escalated {
			t.Fatal("escalated too early")
		}
	}
	if !a[len(a)-1].Escalated {
		t.Fatal("the last stall should escalate to the user instead of retrying again")
	}
}

func TestWatchdogForgetsStallsOutsideTheWindow(t *testing.T) {
	f := newWDFixture()
	for i := 0; i < watchdogMaxRecoveries; i++ {
		f.start("s", watchdogStall+time.Second, false)
		f.w.Patrol()
		f.rc.Clear("s")
		f.w.Patrol()
	}
	f.now = f.now.Add(2 * watchdogWindow)
	f.start("s", watchdogStall+time.Second, false)
	f.w.Patrol()
	a := f.got()
	if a[len(a)-1].Escalated {
		t.Fatal("old stalls still counted")
	}
}

func TestWatchdogIgnoresRunsItCannotSee(t *testing.T) {
	f := newWDFixture()
	ctx, cancel := context.WithCancel(context.Background())
	f.rc.SetCtxCancel("blind", cancel) // no activity registered
	f.w.Patrol()
	if len(f.got()) != 0 || ctx.Err() != nil {
		t.Fatal("touched a run with no activity record")
	}
}

func TestToolInFlightCountsAsActivityBracket(t *testing.T) {
	a := newRunActivity()
	a.toolStart()
	if _, in := a.idleSince(time.Now()); !in {
		t.Fatal("a started tool is in flight")
	}
	a.toolEnd()
	if _, in := a.idleSince(time.Now()); in {
		t.Fatal("an ended tool is not in flight")
	}
}

func TestStallWakeReachesTheSessionThroughTheWakeQueue(t *testing.T) {
	p := newWakeProbe()
	w := fastWake(func(string) bool { return true }, p)
	w.say("s1", "[系统巡检] 请继续")
	waitWake(t, p)
	if !strings.Contains(p.calls[0], "请继续") || strings.Contains(p.calls[0], "后台任务完成") {
		t.Fatalf("a watchdog message goes out as it is: %q", p.calls[0])
	}
}

func TestSameReasonFailuresWithDifferentArgumentsGetAHintThenAStop(t *testing.T) {
	g := newFailureGuard()
	var last string
	for i := 1; i <= sameCauseStop; i++ {
		args := `{"path":"/tmp/f` + itoa(i) + `"}`
		last = g.after("read_file", args, "[tool error] open /tmp/f"+itoa(i)+": permission denied", true)
		switch i {
		case sameCauseHint:
			if !strings.Contains(last, "[hint]") {
				t.Fatalf("attempt %d should carry a hint: %q", i, last)
			}
		case sameCauseStop:
			if !strings.Contains(last, "[stop-loss]") {
				t.Fatalf("attempt %d should say stop: %q", i, last)
			}
		default:
			if strings.Contains(last, "[hint]") || strings.Contains(last, "[stop-loss]") {
				t.Fatalf("attempt %d warned too early: %q", i, last)
			}
		}
	}
}

func TestADifferentFailureOrASuccessRestartsTheCauseCount(t *testing.T) {
	g := newFailureGuard()
	for i := 0; i < sameCauseHint-1; i++ {
		g.after("t", `{"i":`+itoa(i)+`}`, "[tool error] denied", true)
	}
	g.after("t", `{"i":9}`, "ok", false)
	if out := g.after("t", `{"i":10}`, "[tool error] denied", true); strings.Contains(out, "[hint]") {
		t.Fatalf("a success should have reset the streak: %q", out)
	}
	g2 := newFailureGuard()
	for i := 0; i < sameCauseHint-1; i++ {
		g2.after("t", `{"i":`+itoa(i)+`}`, "[tool error] denied", true)
	}
	if out := g2.after("t", `{"i":9}`, "[tool error] timeout", true); strings.Contains(out, "[hint]") {
		t.Fatalf("a different reason should restart the streak: %q", out)
	}
}
