package agent

import (
	"fmt"
	"sync"
	"time"
)

// The watchdog patrols running sessions for a run that has stopped making
// progress: a model stream that hung, a tool that never returned. Nothing else
// notices, because from outside the run is "running" and the user is not
// necessarily looking.
//
// A run that has shown no sign of life for too long is stopped, and the session
// gets one turn that says what happened and asks the agent to pick the work up
// again. A session that keeps stalling is not retried forever: after
// watchdogMaxRecoveries stalls inside watchdogWindow the run is stopped and the
// user is told, which is the point where a person has to look.

const (
	watchdogInterval      = 20 * time.Second
	watchdogStall         = 6 * time.Minute  // no event, chunk or tool activity while waiting on the model
	watchdogToolCap       = 45 * time.Minute // a tool in flight may legitimately take long; this is the ceiling
	watchdogMaxRecoveries = 2
	watchdogWindow        = time.Hour
)

// WatchdogAction is what the watchdog decided for one session.
type WatchdogAction struct {
	SessionID string
	Escalated bool // true: stopped for good, tell the user; false: stopped, ask the agent to continue
	Idle      time.Duration
	InTool    bool
	Text      string
}

// Watchdog stops stalled runs and reports them through Notify.
type Watchdog struct {
	r        *Runner
	interval time.Duration
	stall    time.Duration
	toolCap  time.Duration
	maxRec   int
	window   time.Duration
	now      func() time.Time

	mu         sync.Mutex
	recoveries map[string][]time.Time
	handled    map[*runActivity]struct{}
	notify     func(WatchdogAction)

	stop chan struct{}
	done chan struct{}
}

func newWatchdog(r *Runner) *Watchdog {
	return &Watchdog{
		r: r, interval: watchdogInterval, stall: watchdogStall, toolCap: watchdogToolCap,
		maxRec: watchdogMaxRecoveries, window: watchdogWindow, now: time.Now,
		recoveries: map[string][]time.Time{}, handled: map[*runActivity]struct{}{},
	}
}

// Patrol makes one pass over the running sessions.
func (w *Watchdog) Patrol() {
	if w == nil || w.r == nil || w.r.runControl == nil {
		return
	}
	now := w.now()
	live := map[*runActivity]struct{}{}
	for _, rs := range w.r.runControl.Running() {
		act := w.r.runControl.Activity(rs.SessionID)
		if act == nil {
			continue // not a run the watchdog can see into
		}
		live[act] = struct{}{}
		idle, inTool := act.idleSince(now)
		limit := w.stall
		if inTool {
			limit = w.toolCap
		}
		if idle < limit {
			continue
		}
		w.mu.Lock()
		_, already := w.handled[act]
		w.handled[act] = struct{}{}
		w.mu.Unlock()
		if already {
			continue // stopped already; the run is just taking a moment to unwind
		}
		w.act(rs.SessionID, idle, inTool, now)
	}
	// Forget runs that have ended.
	w.mu.Lock()
	for a := range w.handled {
		if _, ok := live[a]; !ok {
			delete(w.handled, a)
		}
	}
	w.mu.Unlock()
}

func (w *Watchdog) act(session string, idle time.Duration, inTool bool, now time.Time) {
	w.mu.Lock()
	recent := w.recoveries[session][:0:0]
	for _, t := range w.recoveries[session] {
		if now.Sub(t) < w.window {
			recent = append(recent, t)
		}
	}
	escalate := len(recent) >= w.maxRec
	if !escalate {
		recent = append(recent, now)
	}
	w.recoveries[session] = recent
	notify := w.notify
	w.mu.Unlock()

	w.r.runControl.CancelSession(session)

	what := "模型长时间没有任何输出"
	if inTool {
		what = "一个工具调用长时间没有返回"
	}
	a := WatchdogAction{SessionID: session, Escalated: escalate, Idle: idle, InTool: inTool}
	if escalate {
		a.Text = fmt.Sprintf("巡检：这个会话在 %s 内已经第 %d 次卡住（这次是%s，已 %s 没有进展），已停止运行，不再自动重试。请检查模型服务或相关命令后再继续。",
			w.window, len(recent)+1, what, idle.Round(time.Second))
	} else {
		a.Text = fmt.Sprintf("[系统巡检] 上一轮运行因为%s（已 %s 没有任何进展）被中止。请从中断的地方继续：先确认当前状态（已经完成了什么、哪些文件或命令的结果还在），再做剩下的事；如果中断的是某个命令，换一种更稳妥的方式，别原样重来。",
			what, idle.Round(time.Second))
	}
	if notify != nil {
		notify(a)
	}
}

// Run patrols until Stop.
func (w *Watchdog) Run() {
	defer close(w.done)
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
			w.Patrol()
		}
	}
}

// StartWatchdog begins patrolling and routes its decisions: a recoverable stall
// goes to the session as a wake-up turn, an escalation to the escalate callback
// (the host shows it to the user). Safe to call once; later calls are ignored.
func (r *Runner) StartWatchdog(escalate func(WatchdogAction)) {
	if r == nil || r.watchdog != nil {
		return
	}
	w := newWatchdog(r)
	w.notify = func(a WatchdogAction) {
		if a.Escalated {
			if escalate != nil {
				escalate(a)
			}
			return
		}
		r.bgWake.say(a.SessionID, a.Text)
	}
	w.stop, w.done = make(chan struct{}), make(chan struct{})
	r.watchdog = w
	go w.Run()
}

func (r *Runner) stopWatchdog() {
	if r == nil || r.watchdog == nil || r.watchdog.stop == nil {
		return
	}
	select {
	case <-r.watchdog.stop:
	default:
		close(r.watchdog.stop)
	}
	select {
	case <-r.watchdog.done:
	case <-time.After(time.Second):
	}
}
