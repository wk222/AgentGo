package admin

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// AgentRunner runs one admin step via the agent runtime.
type AgentRunner interface {
	RunAdminTask(ctx context.Context, sessionID, prompt string) (string, error)
}

// AdminRunner implements PersistentAdminRunner-style background execution.
type AdminRunner struct {
	store    *Store
	agent    AgentRunner
	ticker   *time.Ticker
	quit     chan struct{}
	stopOnce sync.Once
	onHeal   func(ctx context.Context, appName, errMsg string)
	planner  Planner
}

// Store returns the durable task persistence layer.
func (r *AdminRunner) Store() *Store { return r.store }

// SetPlanner wires LLM or heuristic step planning.
func (r *AdminRunner) SetPlanner(p Planner) {
	if r != nil {
		r.planner = p
	}
}

// EnqueueTask creates a new multi-step durable admin task.
func (r *AdminRunner) EnqueueTask(ctx context.Context, goal string) (*DurableTask, error) {
	steps, err := PlanGoal(ctx, r.planner, goal)
	if err != nil {
		log.Printf("[AdminRunner] plan fallback: %v", err)
		steps = DefaultPlanSteps(goal)
	}
	return r.store.CreateTaskWithSteps(ctx, goal, steps)
}

func NewAdminRunner(store *Store, agent AgentRunner) *AdminRunner {
	return &AdminRunner{
		store: store,
		agent: agent,
		quit:  make(chan struct{}),
	}
}

// SetRuntimeHealHandler wires APP_RUNTIME_ERROR recovery enqueue (optional).
func (r *AdminRunner) SetRuntimeHealHandler(fn func(ctx context.Context, appName, errMsg string)) {
	r.onHeal = fn
}

// EnqueueRuntimeHealTask creates an admin task to fix an app runtime failure.
func (r *AdminRunner) EnqueueRuntimeHealTask(ctx context.Context, appName, errMsg string) (*DurableTask, error) {
	goal := fmt.Sprintf("修复应用运行时错误 APP_RUNTIME_ERROR：应用=%s；错误=%s", appName, truncate(errMsg, 400))
	steps := []string{
		"定位应用 bundle/workflow 配置与最近日志",
		"修复配置或代码并 validate/run 验证",
		"汇报修复结果与回归建议",
	}
	return r.store.CreateTaskWithSteps(ctx, goal, steps)
}

// Start begins the tick loop and recovers stuck running tasks from prior crashes.
func (r *AdminRunner) Start(ctx context.Context, tickInterval time.Duration) {
	if n, err := r.store.RecoverStuckRunning(ctx); err == nil && n > 0 {
		log.Printf("[AdminRunner] recovered %d stuck running task(s) → pending", n)
	}
	r.ticker = time.NewTicker(tickInterval)
	go func() {
		log.Println("[AdminRunner] Started background durable task loop.")
		for {
			select {
				case <-ctx.Done():
					r.Stop()
					return
				case <-r.quit:
					return
				case <-r.ticker.C:
					r.processPendingTasks(ctx)
			}
		}
	}()
}

func (r *AdminRunner) Stop() {
	r.stopOnce.Do(func() {
		if r.ticker != nil {
			r.ticker.Stop()
		}
		close(r.quit)
		log.Println("[AdminRunner] Stopped background durable task loop.")
	})
}

func (r *AdminRunner) processPendingTasks(ctx context.Context) {
	tasks, err := r.store.GetPendingOrRunningTasks(ctx)
	if err != nil {
		log.Printf("[AdminRunner] Failed to fetch tasks: %v", err)
		return
	}
	for _, task := range tasks {
		// Verify latest DB state before tick execution in case it was paused/cancelled
		latest, err := r.store.GetTask(ctx, task.ID)
		if err == nil {
			if latest.Status == StatusPaused || latest.Status == StatusCancelled {
				continue
			}
		}
		r.runOneTask(ctx, task)
	}
}

func (r *AdminRunner) runOneTask(ctx context.Context, task DurableTask) {
	steps := task.Steps
	if len(steps) == 0 {
		steps = DefaultPlanSteps(task.Goal)
	}
	idx := task.CurrentStep
	if idx >= len(steps) {
		_ = r.store.UpdateStatus(ctx, task.ID, StatusCompleted, "")
		return
	}

	stepText := steps[idx]
	log.Printf("[AdminRunner] Task %s step %d/%d: %s", task.ID, idx+1, len(steps), truncate(stepText, 80))

	_ = r.store.UpdateStatus(ctx, task.ID, StatusRunning, "")
	prompt := fmt.Sprintf("[管理员任务 %s]\n总目标：%s\n当前步骤（%d/%d）：%s\n请完成本步骤并给出简要执行报告。",
		task.ID, task.Goal, idx+1, len(steps), stepText)

	startTime := time.Now()
	result, runErr := r.agent.RunAdminTask(ctx, task.ID, prompt)
	duration := time.Since(startTime).Milliseconds()

	// Capture step trace logs
	errMsg := ""
	if runErr != nil {
		errMsg = runErr.Error()
	}
	trace := StepTrace{
		TaskID:     task.ID,
		StepIndex:  idx,
		Action:     "run_step",
		Input:      prompt,
		Output:     result,
		DurationMS: duration,
		Error:      errMsg,
	}
	_ = r.store.SaveStepTrace(ctx, trace)

	if runErr != nil {
		log.Printf("[AdminRunner] Task %s step failed: %v", task.ID, runErr)
		if IsRuntimeHealError(runErr, result) {
			newSteps, err := r.ReplanGoalAfterFailure(ctx, task.Goal, stepText, runErr.Error())
			if err != nil {
				log.Printf("[AdminRunner] LLM replan failed: %v. Fallback to heuristic.", err)
				newSteps = ReplanAfterFailure(task.Goal, stepText, runErr.Error())
			}

			// Trace the replanning decision
			replanTrace := StepTrace{
				TaskID:     task.ID,
				StepIndex:  idx,
				Action:     "replan",
				Input:      fmt.Sprintf("Failed step: %s\nError: %s", stepText, errMsg),
				Output:     fmt.Sprintf("Replanned steps: %v", newSteps),
				DurationMS: time.Since(startTime).Milliseconds(),
			}
			_ = r.store.SaveStepTrace(ctx, replanTrace)

			_ = r.store.ReplanSteps(ctx, task.ID, newSteps, StatusPending)
			log.Printf("[AdminRunner] Task %s replanned (%d steps)", task.ID, len(newSteps))
			return
		}
		_ = r.store.UpdateStatus(ctx, task.ID, StatusFailed, runErr.Error())
		return
	}

	if IsRuntimeHealError(nil, result) {
		newSteps, err := r.ReplanGoalAfterFailure(ctx, task.Goal, stepText, result)
		if err != nil {
			log.Printf("[AdminRunner] LLM replan failed: %v. Fallback to heuristic.", err)
			newSteps = ReplanAfterFailure(task.Goal, stepText, result)
		}

		replanTrace := StepTrace{
			TaskID:     task.ID,
			StepIndex:  idx,
			Action:     "replan",
			Input:      fmt.Sprintf("Failed step (in result): %s", stepText),
			Output:     fmt.Sprintf("Replanned steps: %v", newSteps),
			DurationMS: time.Since(startTime).Milliseconds(),
		}
		_ = r.store.SaveStepTrace(ctx, replanTrace)

		_ = r.store.ReplanSteps(ctx, task.ID, newSteps, StatusPending)
		log.Printf("[AdminRunner] Task %s replanned after runtime error in output", task.ID)
		return
	}

	next := idx + 1
	if next >= len(steps) {
		log.Printf("[AdminRunner] Task %s completed (%d chars final)", task.ID, len(result))
		_ = r.store.AdvanceStep(ctx, task.ID, next, StatusCompleted)
		return
	}
	_ = r.store.AdvanceStep(ctx, task.ID, next, StatusPending)
	log.Printf("[AdminRunner] Task %s advanced to step %d", task.ID, next+1)
}

func (r *AdminRunner) ReplanGoalAfterFailure(ctx context.Context, goal, failedStep, errMsg string) ([]string, error) {
	if r.planner == nil {
		return ReplanAfterFailure(goal, failedStep, errMsg), nil
	}
	return r.planner.ReplanSteps(ctx, goal, failedStep, errMsg)
}

// Pause suspends a running or pending task.
func (r *AdminRunner) Pause(ctx context.Context, taskID string) error {
	task, err := r.store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if err := AssertTransition(task.Status, StatusPaused); err != nil {
		return err
	}
	return r.store.UpdateStatus(ctx, taskID, StatusPaused, "")
}

// Resume restarts a suspended task.
func (r *AdminRunner) Resume(ctx context.Context, taskID string) error {
	task, err := r.store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if err := AssertTransition(task.Status, StatusPending); err != nil {
		return err
	}
	return r.store.UpdateStatus(ctx, taskID, StatusPending, "")
}

// Cancel terminates a task.
func (r *AdminRunner) Cancel(ctx context.Context, taskID string) error {
	task, err := r.store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if err := AssertTransition(task.Status, StatusCancelled); err != nil {
		return err
	}
	return r.store.UpdateStatus(ctx, taskID, StatusCancelled, "")
}

// Retry resets a failed or cancelled task back to pending.
func (r *AdminRunner) Retry(ctx context.Context, taskID string) error {
	task, err := r.store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if err := AssertTransition(task.Status, StatusPending); err != nil {
		return err
	}
	return r.store.UpdateStatus(ctx, taskID, StatusPending, "")
}

// TakeOver sets human resolution for the current step and advances task execution.
func (r *AdminRunner) TakeOver(ctx context.Context, taskID string, humanResult string) error {
	task, err := r.store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	idx := task.CurrentStep
	steps := task.Steps
	if len(steps) == 0 {
		steps = DefaultPlanSteps(task.Goal)
	}
	if idx >= len(steps) {
		return fmt.Errorf("task already completed")
	}

	trace := StepTrace{
		TaskID:     taskID,
		StepIndex:  idx,
		Action:     "human_takeover",
		Input:      steps[idx],
		Output:     humanResult,
		DurationMS: 0,
		CreatedAt:  time.Now().Unix(),
	}
	_ = r.store.SaveStepTrace(ctx, trace)

	next := idx + 1
	if next >= len(steps) {
		return r.store.AdvanceStep(ctx, taskID, next, StatusCompleted)
	}
	return r.store.AdvanceStep(ctx, taskID, next, StatusPending)
}

// DiagnoseStuck checks if the task is taking too long or loops.
func (r *AdminRunner) DiagnoseStuck(ctx context.Context, taskID string) string {
	task, err := r.store.GetTask(ctx, taskID)
	if err != nil {
		return "Task not found"
	}
	if task.Status != StatusRunning {
		return fmt.Sprintf("Task is not running (status: %s)", task.Status)
	}
	// Heuristic stuck check: has it updated in the last 15 minutes?
	if time.Now().Unix()-task.UpdatedAt > 900 {
		return "Task has been running for over 15 minutes without progress; it may be stuck in a tool execution or loop."
	}
	return "Task appears healthy and active."
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// GetActiveTaskGoals feeds SessionSpine durable_tasks section.
func (r *AdminRunner) GetActiveTaskGoals(ctx context.Context) []string {
	tasks, err := r.store.GetPendingOrRunningTasks(ctx)
	if err != nil {
		return nil
	}
	var goals []string
	for _, t := range tasks {
		// Ignore paused and cancelled tasks in active prompt injection context
		if t.Status == StatusPaused || t.Status == StatusCancelled {
			continue
		}
		steps := t.Steps
		if len(steps) == 0 {
			goals = append(goals, t.Goal)
			continue
		}
		idx := t.CurrentStep
		if idx >= len(steps) {
			idx = len(steps) - 1
		}
		goals = append(goals, fmt.Sprintf("%s [步骤 %d/%d] %s", t.ID, idx+1, len(steps), steps[idx]))
	}
	return goals
}
