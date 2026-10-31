package tools

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func invokeMonitoredJob(t *testing.T, r *Registry, in jobMonitorInput) jobMonitorOutput {
	t.Helper()
	data, _ := json.Marshal(in)
	raw, err := r.InvokeJSON(context.Background(), "job_monitor", string(data))
	if err != nil {
		t.Fatal(err)
	}
	var out jobMonitorOutput
	if err = json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err, raw)
	}
	return out
}

func TestJobCancellationRequestDoesNotInventFinishedOutcome(t *testing.T) {
	r := NewRegistry()
	if err := RegisterJobMonitorTool(r, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	mon := r.jobMonitor()
	cancelled := false
	mon.jobs["pending"] = &MonitoredJob{ID: "pending", Status: "running", ExitCode: -1, cancelFn: func() { cancelled = true }}
	out := invokeMonitoredJob(t, r, jobMonitorInput{Action: "cancel", JobID: "pending"})
	if !out.Success || !cancelled || out.Job.Status != "cancelling" || !out.Job.FinishedAt.IsZero() || out.Job.ExitCode != -1 {
		t.Fatalf("premature terminal outcome: %+v", out.Job)
	}
}

func TestMonitoredJobVerboseOutputPreservesFinalUnicodeAndExitCode(t *testing.T) {
	r := NewRegistry()
	if err := RegisterJobMonitorTool(r, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer r.StopJobs(time.Second)
	command := "i=0; while [ $i -lt 2000 ]; do printf 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx'; i=$((i+1)); done; printf '\\344\\270\\255\\346\\226\\207FINAL'; exit 7"
	if runtime.GOOS == "windows" {
		command = "[Console]::OutputEncoding=[Text.UTF8Encoding]::new(); [Console]::Write(('x' * 2000000)); [Console]::Write(([char]0x4e2d).ToString()+[char]0x6587+'FINAL'); exit 7"
	}
	started := invokeMonitoredJob(t, r, jobMonitorInput{Action: "start", Command: command})
	if !started.Success {
		t.Fatal(started)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		got := invokeMonitoredJob(t, r, jobMonitorInput{Action: "status", JobID: started.Job.ID}).Job
		if got.Status == "running" {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if got.Status != "failed" || got.ExitCode != 7 || got.FinishedAt.IsZero() || !utf8.ValidString(got.LastOutputTail) || !strings.Contains(got.LastOutputTail, "\u4e2d\u6587FINAL") || len([]rune(got.LastOutputTail)) > 1500 {
			t.Fatalf("incorrect final outcome: %+v", got)
		}
		return
	}
	t.Fatal("verbose command did not finish")
}
