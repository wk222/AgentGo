package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A job whose command starts a child (not only the shell): the child writes
// started.txt at once and marker.txt three seconds later.
func jobWithChild() string {
	if runtime.GOOS == "windows" {
		return `& powershell -NoProfile -Command "Set-Content -Path started.txt -Value x; Start-Sleep 3; Set-Content -Path marker.txt -Value x"`
	}
	return `(touch started.txt; sleep 3; touch marker.txt)`
}

func fileAppears(path string, within time.Duration) bool {
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

func startJob(t *testing.T, r *Registry, command string) {
	t.Helper()
	res, err := r.InvokeJSON(context.Background(), "job_monitor",
		`{"action":"start","name":"x","command":`+jsonString(command)+`}`)
	if err != nil || !strings.Contains(res, "已在后台静默挂载") {
		t.Fatalf("start: %v %s", err, res)
	}
}

func jsonString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// Closing the registry's jobs ends them and what they started, does not wake
// the agent for jobs it stopped itself, and refuses new work.
func TestStopJobsEndsJobsAndTheirChildren(t *testing.T) {
	ws := t.TempDir()
	r := NewRegistry()
	if err := RegisterJobMonitorTool(r, ws); err != nil {
		t.Fatal(err)
	}
	var woken int32
	SetJobWakeupHandler(func(*MonitoredJob) { atomic.AddInt32(&woken, 1) })
	t.Cleanup(func() { SetJobWakeupHandler(nil) })

	startJob(t, r, jobWithChild())
	if !fileAppears(filepath.Join(ws, "started.txt"), 15*time.Second) {
		t.Fatal("the job's child never started; the test cannot tell anything")
	}

	begin := time.Now()
	if n := r.StopJobs(5 * time.Second); n != 1 {
		t.Errorf("StopJobs stopped %d jobs, want 1", n)
	}
	if d := time.Since(begin); d > 4*time.Second {
		t.Errorf("StopJobs took %v", d)
	}
	list, _ := r.InvokeJSON(context.Background(), "job_monitor", `{"action":"list"}`)
	if !strings.Contains(list, `"cancelled"`) {
		t.Errorf("the job should be reported cancelled: %s", list)
	}
	if atomic.LoadInt32(&woken) != 0 {
		t.Error("a job stopped by shutdown must not wake the agent")
	}
	if fileAppears(filepath.Join(ws, "marker.txt"), 5*time.Second) {
		t.Fatal("a process started by the job survived StopJobs")
	}

	res, _ := r.InvokeJSON(context.Background(), "job_monitor", `{"action":"start","command":"echo hi"}`)
	if !strings.Contains(res, "shutting down") {
		t.Errorf("a new job must be refused after StopJobs: %s", res)
	}
	if n := r.StopJobs(time.Second); n != 0 {
		t.Errorf("second StopJobs stopped %d", n)
	}
}

// Jobs belong to the registry that started them; stopping one registry's jobs
// leaves another's alone (the state used to be package-global).
func TestStopJobsOnlyAffectsItsOwnRegistry(t *testing.T) {
	wsA, wsB := t.TempDir(), t.TempDir()
	a, b := NewRegistry(), NewRegistry()
	for _, p := range []struct {
		r  *Registry
		ws string
	}{{a, wsA}, {b, wsB}} {
		if err := RegisterJobMonitorTool(p.r, p.ws); err != nil {
			t.Fatal(err)
		}
		startJob(t, p.r, jobWithChild())
		if !fileAppears(filepath.Join(p.ws, "started.txt"), 15*time.Second) {
			t.Fatal("job never started")
		}
	}
	a.StopJobs(5 * time.Second)

	if listB, _ := b.InvokeJSON(context.Background(), "job_monitor", `{"action":"list"}`); !strings.Contains(listB, `"running"`) {
		t.Errorf("stopping A's jobs must not touch B's: %s", listB)
	}
	if !fileAppears(filepath.Join(wsB, "marker.txt"), 8*time.Second) {
		t.Error("B's job was killed by A's StopJobs")
	}
	listA, _ := a.InvokeJSON(context.Background(), "job_monitor", `{"action":"list"}`)
	if strings.Contains(listA, wsB) || strings.Count(listA, `"id"`) != 1 {
		t.Errorf("A sees jobs that are not its own: %s", listA)
	}
	b.StopJobs(5 * time.Second)
}

func TestStopJobsOnANilOrIdleRegistry(t *testing.T) {
	var nilReg *Registry
	if n := nilReg.StopJobs(time.Second); n != 0 {
		t.Errorf("nil registry: %d", n)
	}
	if n := NewRegistry().StopJobs(time.Second); n != 0 {
		t.Errorf("idle registry: %d", n)
	}
}
