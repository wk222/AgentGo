package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/governance"
)

// ScheduleRequest asks the host to create a recurring or one-shot scheduled task.
type ScheduleRequest struct {
	Spec       string `json:"spec"`
	Prompt     string `json:"prompt"`
	Title      string `json:"title,omitempty"`
	TZ         string `json:"tz,omitempty"`
	SessionID  string `json:"session_id"`            // the conversation whose agent is asking
	NewSession bool   `json:"new_session,omitempty"` // open a fresh conversation on every run
}

// ScheduleHandler is implemented by the host (bridge) that owns the scheduler.
type ScheduleHandler interface {
	CreateSchedule(ctx context.Context, req ScheduleRequest) (any, error)
	ListSchedules(ctx context.Context, sessionID string) (any, error)
	DeleteSchedule(ctx context.Context, sessionID, id string) error
	SetScheduleEnabled(ctx context.Context, sessionID, id string, enabled bool) error
	RunScheduleNow(ctx context.Context, sessionID, id string) (any, error)
}

var (
	scheduleMu      sync.RWMutex
	scheduleHandler ScheduleHandler
)

// SetScheduleHandler wires the scheduler into the schedule_task tool.
func SetScheduleHandler(h ScheduleHandler) {
	scheduleMu.Lock()
	scheduleHandler = h
	scheduleMu.Unlock()
}

const maxSchedulePrompt = 8000

type scheduleTaskInput struct {
	Action          string `json:"action" jsonschema:"description=Action: 'create'\\, 'list'\\, 'delete'\\, 'pause'\\, 'resume' or 'run_now'"`
	Spec            string `json:"spec,omitempty" jsonschema:"description=When to run (action=create). Recurring: '每30分钟' / 'every 2h' / '每天9点' / '每周一 9:30' / '工作日 8:30' / 'cron:0 9 * * 1-5' (5-field cron: lists\\, ranges\\, steps\\, day names). One-shot: '30分钟后' / 'in 2h' / 'at:2026-11-05 09:30'. Minimum interval is 1 minute."`
	Prompt          string `json:"prompt,omitempty" jsonschema:"description=What you should do each time it fires\\, written as a self-contained instruction (you will not remember this conversation unless new_conversation is false)"`
	Title           string `json:"title,omitempty" jsonschema:"description=Short label shown in the task list"`
	Timezone        string `json:"timezone,omitempty" jsonschema:"description=IANA zone for the wall-clock times in spec\\, e.g. Asia/Shanghai. Default: the machine's local zone"`
	NewConversation bool   `json:"new_conversation,omitempty" jsonschema:"description=true: every run opens a brand-new conversation (independent periodic reports). Default false: runs continue THIS conversation and keep its context"`
	ID              string `json:"id,omitempty" jsonschema:"description=Task id for delete/pause/resume/run_now"`
}

type scheduleTaskOutput struct {
	Success bool   `json:"success"`
	Action  string `json:"action"`
	Result  any    `json:"result,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// RegisterScheduleTaskTool registers `schedule_task`: the agent can set up timed work for itself
// (a daily report, a reminder in 30 minutes, a weekly check) and then finish its turn; the host
// starts a new turn when the time comes, even after the app was restarted.
func RegisterScheduleTaskTool(r *Registry) error {
	t, err := utils.InferTool("schedule_task",
		"Schedule work for LATER: a recurring job (every N minutes, daily at 9:00, weekdays, cron) or a one-shot reminder (in 30 minutes, at a date). "+
			"Use it whenever the user asks for a reminder, a regular check or report, or anything to happen later or on a timetable. "+
			"When the time comes a new turn starts with your prompt, in this conversation by default so you keep the context. "+
			"Survives restarts. For 'wake me when something happens' (a file, a log line, a process exit) use watch_event instead.",
		func(ctx context.Context, in scheduleTaskInput) (scheduleTaskOutput, error) {
			scheduleMu.RLock()
			h := scheduleHandler
			scheduleMu.RUnlock()
			action := strings.ToLower(strings.TrimSpace(in.Action))
			if h == nil {
				return scheduleTaskOutput{Action: action, Error: "scheduled tasks are not available in this host"}, nil
			}
			sid := governance.SessionIDFromContext(ctx)
			fail := func(err error) (scheduleTaskOutput, error) {
				return scheduleTaskOutput{Action: action, Error: err.Error()}, nil
			}
			id := strings.TrimSpace(in.ID)
			needID := func() bool { return id != "" }

			switch action {
			case "create":
				prompt := strings.TrimSpace(in.Prompt)
				if strings.TrimSpace(in.Spec) == "" || prompt == "" {
					return fail(fmt.Errorf("spec and prompt are required"))
				}
				if len([]rune(prompt)) > maxSchedulePrompt {
					return fail(fmt.Errorf("prompt is too long (max %d characters)", maxSchedulePrompt))
				}
				res, err := h.CreateSchedule(ctx, ScheduleRequest{
					Spec: strings.TrimSpace(in.Spec), Prompt: prompt, Title: strings.TrimSpace(in.Title),
					TZ: strings.TrimSpace(in.Timezone), SessionID: sid, NewSession: in.NewConversation,
				})
				if err != nil {
					return fail(err)
				}
				return scheduleTaskOutput{Success: true, Action: action, Result: res,
					Message: "已创建。到点会自动开始新一轮；现在可以结束本轮对话。"}, nil
			case "list":
				res, err := h.ListSchedules(ctx, sid)
				if err != nil {
					return fail(err)
				}
				return scheduleTaskOutput{Success: true, Action: action, Result: res}, nil
			case "delete", "cancel":
				if !needID() {
					return fail(fmt.Errorf("id is required"))
				}
				if err := h.DeleteSchedule(ctx, sid, id); err != nil {
					return fail(err)
				}
				return scheduleTaskOutput{Success: true, Action: action, Message: "已删除"}, nil
			case "pause", "resume":
				if !needID() {
					return fail(fmt.Errorf("id is required"))
				}
				if err := h.SetScheduleEnabled(ctx, sid, id, action == "resume"); err != nil {
					return fail(err)
				}
				return scheduleTaskOutput{Success: true, Action: action}, nil
			case "run_now":
				if !needID() {
					return fail(fmt.Errorf("id is required"))
				}
				res, err := h.RunScheduleNow(ctx, sid, id)
				if err != nil {
					return fail(err)
				}
				return scheduleTaskOutput{Success: true, Action: action, Result: res, Message: "已开始执行一次"}, nil
			default:
				return fail(fmt.Errorf("unknown action %q, use create/list/delete/pause/resume/run_now", in.Action))
			}
		},
	)
	if err != nil {
		return err
	}
	// Static: "提醒我 / 每天 9 点 / 半小时后" never names a tool. Behind tool_search the model
	// (qwen3.7-max, measured) answers "scheduled tasks are not supported" without searching.
	Declare(Spec{Name: "schedule_task", Source: "builtin", Static: true, Risk: governance.RiskMedium})
	r.AddTool(t)
	return nil
}
