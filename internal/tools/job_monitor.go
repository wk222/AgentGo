package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/shellcmd"
)

// MonitoredJob represents an experiment or long-running script being watched in the background.
type MonitoredJob struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Command        string    `json:"command"`
	Status         string    `json:"status"` // "running", "completed", "failed", "cancelled"
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at,omitempty"`
	ExitCode       int       `json:"exit_code"`
	WatchFile      string    `json:"watch_file,omitempty"`
	SummaryPrompt  string    `json:"summary_prompt,omitempty"`
	LastOutputTail string    `json:"last_output_tail,omitempty"`
	cancelFn       context.CancelFunc
}

// snapshot is a copy that no goroutine writes to. Everything handed outside the
// monitor (tool output, the wake-up callback) is a snapshot taken under the lock:
// the live job is changed by its worker, and the caller serializes or reads what
// it was given later.
func (j *MonitoredJob) snapshot() *MonitoredJob {
	c := *j
	c.cancelFn = nil
	return &c
}

type jobMonitorInput struct {
	Action        string `json:"action" jsonschema:"description=Action: 'start', 'status', 'list', or 'cancel'"`
	JobID         string `json:"job_id,omitempty" jsonschema:"description=Job ID for status or cancel action"`
	Name          string `json:"name,omitempty" jsonschema:"description=Descriptive name for the experiment/task"`
	Command       string `json:"command,omitempty" jsonschema:"description=Command or script to execute (e.g. 'python monitor_train.py')"`
	WatchFile     string `json:"watch_file,omitempty" jsonschema:"description=Optional log or metric file to monitor for completion/tail"`
	SummaryPrompt string `json:"summary_prompt,omitempty" jsonschema:"description=Prompt injected to wake up the agent when completed"`
}

type jobMonitorOutput struct {
	Success bool            `json:"success"`
	Action  string          `json:"action"`
	Message string          `json:"message"`
	Job     *MonitoredJob   `json:"job,omitempty"`
	Jobs    []*MonitoredJob `json:"jobs,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// jobMonitor owns the background jobs started through one Registry: it knows
// them, can stop them, and waits for their goroutines. State used to be
// package-global, so any registry could see (and stop) any other's jobs and
// nothing could end them all on exit.
type jobMonitor struct {
	mu      sync.RWMutex
	jobs    map[string]*MonitoredJob
	counter int64
	closed  bool
	wg      sync.WaitGroup
}

var (
	wakeMu      sync.RWMutex
	onJobWakeup func(job *MonitoredJob)
)

// SetJobWakeupHandler configures the callback invoked when a background experiment finishes.
func SetJobWakeupHandler(fn func(job *MonitoredJob)) {
	wakeMu.Lock()
	defer wakeMu.Unlock()
	onJobWakeup = fn
}

func (r *Registry) jobMonitor() *jobMonitor {
	r.jobsOnce.Do(func() { r.jobs = &jobMonitor{jobs: make(map[string]*MonitoredJob)} })
	return r.jobs
}

// StopJobs ends every background job this registry started (the whole process
// tree of each), refuses new ones, and waits up to wait for them to finish. The
// jobs' wake-up callbacks do not fire: nobody is left to wake. Returns how many
// jobs it stopped. Idempotent.
func (r *Registry) StopJobs(wait time.Duration) int {
	if r == nil {
		return 0
	}
	m := r.jobMonitor()
	m.mu.Lock()
	m.closed = true
	stopped := 0
	for _, j := range m.jobs {
		if j.Status == "running" && j.cancelFn != nil {
			j.cancelFn()
			stopped++
		}
	}
	m.mu.Unlock()

	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(wait):
	}
	return stopped
}

// RegisterJobMonitorTool registers the `job_monitor` tool.
// Solves the token-drain problem of long experiments:
// 1. Agent generates/launches an experiment or monitoring script in the background.
// 2. Main dialogue turns immediately end (0 tokens burned during execution).
// 3. When the script exits or finishes, the agent is proactively woken up with results.
func RegisterJobMonitorTool(r *Registry, workspaceRoot string) error {
	mon := r.jobMonitor()
	t, err := utils.InferTool("job_monitor",
		"Run and monitor long-running experiments or scripts in the background without burning LLM tokens. The agent is automatically woken up with summary output when the job completes or fails.",
		func(ctx context.Context, in jobMonitorInput) (jobMonitorOutput, error) {
			action := strings.ToLower(strings.TrimSpace(in.Action))

			switch action {
			case "start":
				cmdStr := strings.TrimSpace(in.Command)
				if cmdStr == "" {
					return jobMonitorOutput{Success: false, Action: "start", Error: "command is required to start a monitored job"}, nil
				}
				name := strings.TrimSpace(in.Name)
				if name == "" {
					name = fmt.Sprintf("job-%d", time.Now().Unix())
				}

				mon.mu.Lock()
				if mon.closed {
					mon.mu.Unlock()
					return jobMonitorOutput{Success: false, Action: "start", Error: "the application is shutting down; not starting new jobs"}, nil
				}
				mon.counter++
				jobID := fmt.Sprintf("job_%d_%d", time.Now().Unix(), mon.counter)
				jobCtx, cancel := context.WithCancel(context.Background())

				job := &MonitoredJob{
					ID:            jobID,
					Name:          name,
					Command:       cmdStr,
					Status:        "running",
					StartedAt:     time.Now(),
					WatchFile:     in.WatchFile,
					SummaryPrompt: in.SummaryPrompt,
					cancelFn:      cancel,
				}
				mon.jobs[jobID] = job
				mon.wg.Add(1)
				started := job.snapshot()
				mon.mu.Unlock()

				// Launch background worker goroutine (0 LLM tokens consumed)
				go mon.run(jobCtx, job, workspaceRoot)

				return jobMonitorOutput{
					Success: true,
					Action:  "start",
					Message: fmt.Sprintf("✅ 实验监控任务 [%s] 已在后台静默挂载 (Job ID: %s)。主会话即刻休眠以节省 Token，完成时系统将自动唤醒 Agent 进行分析汇报。", name, jobID),
					Job:     started,
				}, nil

			case "status":
				id := strings.TrimSpace(in.JobID)
				if id == "" {
					return jobMonitorOutput{Success: false, Action: "status", Error: "job_id required"}, nil
				}
				mon.mu.RLock()
				live, exists := mon.jobs[id]
				var job *MonitoredJob
				if exists {
					job = live.snapshot()
				}
				mon.mu.RUnlock()
				if !exists {
					return jobMonitorOutput{Success: false, Action: "status", Error: "job not found: " + id}, nil
				}
				return jobMonitorOutput{
					Success: true,
					Action:  "status",
					Job:     job,
					Message: fmt.Sprintf("任务 [%s] 当前状态: %s", job.Name, job.Status),
				}, nil

			case "list":
				mon.mu.RLock()
				defer mon.mu.RUnlock()
				var list []*MonitoredJob
				for _, j := range mon.jobs {
					list = append(list, j.snapshot())
				}
				return jobMonitorOutput{
					Success: true,
					Action:  "list",
					Jobs:    list,
					Message: fmt.Sprintf("当前总共托管 %d 个后台任务", len(list)),
				}, nil

			case "cancel":
				id := strings.TrimSpace(in.JobID)
				if id == "" {
					return jobMonitorOutput{Success: false, Action: "cancel", Error: "job_id required"}, nil
				}
				mon.mu.Lock()
				live, exists := mon.jobs[id]
				var job *MonitoredJob
				if exists {
					if live.Status == "running" {
						if live.cancelFn != nil {
							live.cancelFn()
						}
						live.Status = "cancelled"
						live.FinishedAt = time.Now()
					}
					job = live.snapshot()
				}
				mon.mu.Unlock()
				if !exists {
					return jobMonitorOutput{Success: false, Action: "cancel", Error: "job not found: " + id}, nil
				}
				return jobMonitorOutput{
					Success: true,
					Action:  "cancel",
					Job:     job,
					Message: fmt.Sprintf("任务 [%s] 已被成功取消", id),
				}, nil

			default:
				return jobMonitorOutput{
					Success: false,
					Action:  in.Action,
					Error:   fmt.Sprintf("未知操作 '%s'，有效动作为: start, status, list, cancel", in.Action),
				}, nil
			}
		},
	)
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}

func (m *jobMonitor) run(ctx context.Context, job *MonitoredJob, workspaceRoot string) {
	defer m.wg.Done()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", job.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", job.Command)
	}
	if workspaceRoot != "" {
		cmd.Dir = workspaceRoot
	}
	// Cancelling must end what the command started, not only the shell, and must
	// not wait forever for a grandchild that still holds the output pipe.
	shellcmd.Prepare(cmd)
	cmd.Cancel = func() error { shellcmd.KillTree(cmd); return nil }
	cmd.WaitDelay = 2 * time.Second

	// Capture combined output
	outputBytes, err := cmd.CombinedOutput()
	outStr := string(outputBytes)

	m.mu.Lock()
	// Keep last 1500 characters as tail summary
	if len(outStr) > 1500 {
		job.LastOutputTail = outStr[len(outStr)-1500:]
	} else {
		job.LastOutputTail = outStr
	}
	job.FinishedAt = time.Now()
	if err != nil {
		if ctx.Err() == context.Canceled {
			job.Status = "cancelled"
		} else {
			job.Status = "failed"
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			job.ExitCode = exitErr.ExitCode()
		} else {
			job.ExitCode = -1
		}
	} else {
		job.Status = "completed"
		job.ExitCode = 0
	}
	closed := m.closed
	final := job.snapshot()
	m.mu.Unlock()

	// Proactively trigger the wake-up callback to resume/notify the Agent. Not
	// during shutdown: the jobs were stopped because nobody is left to wake.
	wakeMu.RLock()
	handler := onJobWakeup
	wakeMu.RUnlock()
	if handler != nil && !closed {
		handler(final)
	}
}

// ReadWatchFileTail reads the last lines of an experiment watch file.
func ReadWatchFileTail(filePath string, maxLines int) string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) <= maxLines {
		return string(data)
	}
	return strings.Join(lines[len(lines)-maxLines:], "\n")
}
