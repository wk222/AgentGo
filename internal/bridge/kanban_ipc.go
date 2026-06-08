package bridge

import (
	"context"

	"agentgo/internal/kanban"
	"agentgo/internal/terminal"
)

func (s *AppService) ListKanbanBoards() []map[string]any {
	ctx := context.Background()
	boards, err := s.rt.Kanban().ListBoards(ctx)
	if err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(boards))
	for _, b := range boards {
		n, _ := s.rt.Kanban().TaskCount(ctx, b.ID)
		out = append(out, map[string]any{"id": b.ID, "title": b.Title, "tasks": n})
	}
	return out
}

func (s *AppService) ListKanbanTasks(boardID string) []map[string]any {
	ctx := context.Background()
	if boardID == "" {
		boardID = "default"
	}
	tasks, err := s.rt.Kanban().ListTasks(ctx, boardID)
	if err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, map[string]any{
			"id": t.ID, "title": t.Title, "status": t.Status, "board_id": t.BoardID,
		})
	}
	return out
}

func (s *AppService) UpdateKanbanTaskStatus(taskID, status string) map[string]any {
	ctx := context.Background()
	if err := s.rt.Kanban().UpdateTaskStatus(ctx, taskID, status); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true}
}

func (s *AppService) CreateKanbanTask(boardID, title, status string) map[string]any {
	ctx := context.Background()
	if boardID == "" {
		boardID = "default"
	}
	t := kanban.Task{BoardID: boardID, Title: title, Status: status}
	if err := s.rt.Kanban().CreateTask(ctx, t); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "id": t.ID}
}

func (s *AppService) RunTerminalCommand(command string) map[string]any {
	ctx := context.Background()
	res, err := terminal.Run(ctx, s.rt.WorkspaceRoot(), command)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{
		"success": true, "stdout": res.Stdout, "stderr": res.Stderr,
		"exit_code": res.ExitCode, "duration": res.Duration,
	}
}
