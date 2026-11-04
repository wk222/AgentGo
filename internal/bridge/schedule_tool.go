package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentgo/internal/scheduler"
	"agentgo/internal/tools"
)

const (
	maxSchedulesPerSession = 20
	maxAgentSchedules      = 100
)

// fireScheduled starts the background turn of a scheduled job that came due.
func (r *Runtime) fireScheduled(_ context.Context, j scheduler.Job) {
	if r.taskHub == nil {
		return
	}
	_, _ = r.taskHub.Start("cron", j.SessionID, scheduledInput(j))
}

// scheduledInput is the prompt of a run; one that is being caught up after downtime says so.
func scheduledInput(j scheduler.Job) string {
	if !j.Late || j.ScheduledFor == 0 {
		return j.Prompt
	}
	when := time.Unix(j.ScheduledFor, 0).Format("2006-01-02 15:04")
	return j.Prompt + fmt.Sprintf("\n\n（说明：这次定时任务原定 %s 执行，因程序未运行或电脑休眠，延后到现在才补跑。）", when)
}

// scheduledTitle names the conversation a run opens: the first line of its prompt, shortened.
func scheduledTitle(prompt string) string {
	line := strings.TrimSpace(prompt)
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	if r := []rune(line); len(r) > 24 {
		line = string(r[:24]) + "…"
	}
	if line == "" {
		line = "定时任务"
	}
	return line
}

// --- tools.ScheduleHandler: lets the agent set up timed work for itself ---

func (r *Runtime) scheduleStore() (*scheduler.Store, error) {
	if r.sched == nil {
		return nil, errors.New("scheduler is disabled")
	}
	return r.sched, nil
}

// ownedSchedule returns a job the caller may touch: one its conversation created or runs in.
func (r *Runtime) ownedSchedule(sessionID, id string) (*scheduler.Store, scheduler.Job, error) {
	st, err := r.scheduleStore()
	if err != nil {
		return nil, scheduler.Job{}, err
	}
	j, err := st.Get(strings.TrimSpace(id))
	if err != nil {
		return nil, scheduler.Job{}, fmt.Errorf("no scheduled task %q", id)
	}
	if sessionID == "" || (j.Origin != sessionID && j.SessionID != sessionID) {
		return nil, scheduler.Job{}, errors.New("this task belongs to another conversation")
	}
	return st, j, nil
}

func (r *Runtime) CreateSchedule(_ context.Context, req tools.ScheduleRequest) (any, error) {
	st, err := r.scheduleStore()
	if err != nil {
		return nil, err
	}
	if req.SessionID == "" {
		return nil, errors.New("a scheduled task needs a conversation to belong to")
	}
	existing, err := st.List()
	if err != nil {
		return nil, err
	}
	mine, byAgents := 0, 0
	for _, j := range existing {
		if !j.Enabled || j.Origin == "" {
			continue
		}
		byAgents++
		if j.Origin == req.SessionID {
			mine++
		}
	}
	if mine >= maxSchedulesPerSession {
		return nil, fmt.Errorf("too many active scheduled tasks in this conversation (%d); delete some first", mine)
	}
	// A job that opens a new conversation per run could otherwise breed jobs from each of them.
	if byAgents >= maxAgentSchedules {
		return nil, fmt.Errorf("too many scheduled tasks created by agents (%d); ask the user to clean up", byAgents)
	}
	bound := req.SessionID
	if req.NewSession {
		bound = ""
	}
	return st.CreateWith(scheduler.CreateOptions{
		Title: req.Title, Spec: req.Spec, TZ: req.TZ, Prompt: req.Prompt, SessionID: bound, Origin: req.SessionID,
	})
}

func (r *Runtime) ListSchedules(_ context.Context, sessionID string) (any, error) {
	st, err := r.scheduleStore()
	if err != nil {
		return nil, err
	}
	all, err := st.List()
	if err != nil {
		return nil, err
	}
	out := []scheduler.Job{}
	for _, j := range all {
		if j.Origin == sessionID || j.SessionID == sessionID {
			out = append(out, j)
		}
	}
	return out, nil
}

func (r *Runtime) DeleteSchedule(_ context.Context, sessionID, id string) error {
	st, j, err := r.ownedSchedule(sessionID, id)
	if err != nil {
		return err
	}
	return st.Delete(j.ID)
}

func (r *Runtime) SetScheduleEnabled(_ context.Context, sessionID, id string, enabled bool) error {
	st, j, err := r.ownedSchedule(sessionID, id)
	if err != nil {
		return err
	}
	return st.SetEnabled(j.ID, enabled)
}

func (r *Runtime) RunScheduleNow(_ context.Context, sessionID, id string) (any, error) {
	st, j, err := r.ownedSchedule(sessionID, id)
	if err != nil {
		return nil, err
	}
	if r.taskHub == nil {
		return nil, errors.New("task hub unavailable")
	}
	t, err := r.taskHub.Start("cron", j.SessionID, j.Prompt)
	if err != nil {
		return nil, err
	}
	_ = st.MarkRan(j.ID)
	return map[string]any{"task_id": t.ID}, nil
}
