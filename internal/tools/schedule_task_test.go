package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"agentgo/internal/governance"
)

type fakeSchedule struct {
	created []ScheduleRequest
	owner   map[string]string
	enabled map[string]bool
}

func (f *fakeSchedule) CreateSchedule(_ context.Context, r ScheduleRequest) (any, error) {
	if strings.Contains(r.Spec, "bad") {
		return nil, errors.New("无法解析定时描述")
	}
	f.created = append(f.created, r)
	f.owner["job1"] = r.SessionID
	return map[string]string{"id": "job1"}, nil
}
func (f *fakeSchedule) ListSchedules(_ context.Context, sid string) (any, error) {
	return []string{sid}, nil
}
func (f *fakeSchedule) DeleteSchedule(_ context.Context, sid, id string) error {
	if f.owner[id] != sid {
		return errors.New("this task belongs to another conversation")
	}
	delete(f.owner, id)
	return nil
}
func (f *fakeSchedule) SetScheduleEnabled(_ context.Context, sid, id string, on bool) error {
	if f.owner[id] != sid {
		return errors.New("this task belongs to another conversation")
	}
	f.enabled[id] = on
	return nil
}
func (f *fakeSchedule) RunScheduleNow(_ context.Context, sid, id string) (any, error) {
	return map[string]string{"task_id": "t1"}, nil
}

func newScheduleTool(t *testing.T) (*Registry, *fakeSchedule) {
	t.Helper()
	r := NewRegistry()
	if err := RegisterScheduleTaskTool(r); err != nil {
		t.Fatal(err)
	}
	f := &fakeSchedule{owner: map[string]string{}, enabled: map[string]bool{}}
	SetScheduleHandler(f)
	t.Cleanup(func() { SetScheduleHandler(nil) })
	return r, f
}

func TestScheduleTaskSchema(t *testing.T) {
	r, _ := newScheduleTool(t)
	bt, ok := r.Get("schedule_task")
	if !ok {
		t.Fatal("schedule_task not registered")
	}
	info, err := bt.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sc, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(sc)
	for _, want := range []string{"cron:0 9", "new_conversation", "timezone", "30分钟后"} {
		if !strings.Contains(string(js), want) {
			t.Errorf("the schema lost %q: %s", want, js)
		}
	}
}

func TestScheduleTaskCreateBindsToCallerSession(t *testing.T) {
	r, f := newScheduleTool(t)
	ctx := governance.WithSessionID(context.Background(), "sess-A")
	out := invokeByName(t, r, ctx, "schedule_task",
		`{"action":"create","spec":"每天9点","prompt":"汇总昨天的日志","title":"日报","timezone":"Asia/Shanghai","new_conversation":true}`)
	if !strings.Contains(out, `"success":true`) {
		t.Fatalf("create: %s", out)
	}
	if len(f.created) != 1 {
		t.Fatalf("created %d", len(f.created))
	}
	got := f.created[0]
	if got.SessionID != "sess-A" || got.Spec != "每天9点" || got.TZ != "Asia/Shanghai" || !got.NewSession || got.Title != "日报" {
		t.Fatalf("request: %+v", got)
	}

	other := governance.WithSessionID(context.Background(), "sess-B")
	if out := invokeByName(t, r, other, "schedule_task", `{"action":"delete","id":"job1"}`); strings.Contains(out, `"success":true`) {
		t.Fatalf("another conversation deleted the task: %s", out)
	}
	if out := invokeByName(t, r, ctx, "schedule_task", `{"action":"pause","id":"job1"}`); !strings.Contains(out, `"success":true`) || f.enabled["job1"] {
		t.Fatalf("pause: %s", out)
	}
	if out := invokeByName(t, r, ctx, "schedule_task", `{"action":"resume","id":"job1"}`); !strings.Contains(out, `"success":true`) || !f.enabled["job1"] {
		t.Fatalf("resume: %s", out)
	}
	if out := invokeByName(t, r, ctx, "schedule_task", `{"action":"run_now","id":"job1"}`); !strings.Contains(out, "t1") {
		t.Fatalf("run_now: %s", out)
	}
	if out := invokeByName(t, r, ctx, "schedule_task", `{"action":"delete","id":"job1"}`); !strings.Contains(out, `"success":true`) {
		t.Fatalf("delete: %s", out)
	}
}

func TestScheduleTaskRejectsBadInput(t *testing.T) {
	r, _ := newScheduleTool(t)
	ctx := governance.WithSessionID(context.Background(), "s")
	for name, args := range map[string]string{
		"no spec":         `{"action":"create","prompt":"x"}`,
		"no prompt":       `{"action":"create","spec":"每天9点"}`,
		"prompt too long": `{"action":"create","spec":"每天9点","prompt":"` + strings.Repeat("字", maxSchedulePrompt+1) + `"}`,
		"host rejects":    `{"action":"create","spec":"bad spec","prompt":"x"}`,
		"delete no id":    `{"action":"delete"}`,
		"unknown action":  `{"action":"dance"}`,
	} {
		if out := invokeByName(t, r, ctx, "schedule_task", args); strings.Contains(out, `"success":true`) || !strings.Contains(out, `"error"`) {
			t.Errorf("%s should fail with an error: %s", name, out)
		}
	}
	if out := invokeByName(t, r, ctx, "schedule_task", `{"action":"create","spec":"bad spec","prompt":"x"}`); !strings.Contains(out, "无法解析") {
		t.Errorf("the host's reason must reach the model: %s", out)
	}
}

func TestScheduleTaskWithoutHost(t *testing.T) {
	r := NewRegistry()
	_ = RegisterScheduleTaskTool(r)
	SetScheduleHandler(nil)
	if out := invokeByName(t, r, context.Background(), "schedule_task", `{"action":"list"}`); !strings.Contains(out, "not available") {
		t.Fatalf("got %s", out)
	}
}
