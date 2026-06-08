package bridge

import (
	"context"

	"agentgo/internal/admin"
)

// ListAdminTasks returns a list of recent durable admin tasks.
func (s *AppService) ListAdminTasks(limit, offset int) ([]admin.DurableTask, error) {
	if s.rt.adminRunner == nil {
		return nil, nil
	}
	ctx := context.Background()
	if limit <= 0 {
		limit = 50
	}
	return s.rt.adminRunner.Store().ListAllTasks(ctx, limit, offset)
}

// GetAdminTask fetching a single task by its ID.
func (s *AppService) GetAdminTask(taskID string) (*admin.DurableTask, error) {
	if s.rt.adminRunner == nil {
		return nil, nil
	}
	ctx := context.Background()
	return s.rt.adminRunner.Store().GetTask(ctx, taskID)
}

// GetAdminTaskTraces returns step traces for a given admin task.
func (s *AppService) GetAdminTaskTraces(taskID string) ([]admin.StepTrace, error) {
	if s.rt.adminRunner == nil {
		return nil, nil
	}
	ctx := context.Background()
	return s.rt.adminRunner.Store().ListStepTraces(ctx, taskID)
}

// PauseAdminTask pauses a running or pending admin task.
func (s *AppService) PauseAdminTask(taskID string) error {
	if s.rt.adminRunner == nil {
		return nil
	}
	ctx := context.Background()
	return s.rt.adminRunner.Pause(ctx, taskID)
}

// ResumeAdminTask resumes a paused admin task.
func (s *AppService) ResumeAdminTask(taskID string) error {
	if s.rt.adminRunner == nil {
		return nil
	}
	ctx := context.Background()
	return s.rt.adminRunner.Resume(ctx, taskID)
}

// CancelAdminTask cancels an admin task.
func (s *AppService) CancelAdminTask(taskID string) error {
	if s.rt.adminRunner == nil {
		return nil
	}
	ctx := context.Background()
	return s.rt.adminRunner.Cancel(ctx, taskID)
}

// RetryAdminTask retries a failed or cancelled admin task.
func (s *AppService) RetryAdminTask(taskID string) error {
	if s.rt.adminRunner == nil {
		return nil
	}
	ctx := context.Background()
	return s.rt.adminRunner.Retry(ctx, taskID)
}

// TakeOverAdminTask allows a human to provide the result for the current step.
func (s *AppService) TakeOverAdminTask(taskID string, humanResult string) error {
	if s.rt.adminRunner == nil {
		return nil
	}
	ctx := context.Background()
	return s.rt.adminRunner.TakeOver(ctx, taskID, humanResult)
}

// DiagnoseAdminTask checks if an admin task is stuck.
func (s *AppService) DiagnoseAdminTask(taskID string) string {
	if s.rt.adminRunner == nil {
		return "Admin runner unavailable"
	}
	ctx := context.Background()
	return s.rt.adminRunner.DiagnoseStuck(ctx, taskID)
}
