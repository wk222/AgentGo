package bridge

import (
	"time"

	"agentgo/internal/workspace"
)

func (s *AppService) BeginWorkspaceReview(sessionID string) map[string]any {
	resumed, pending, skipped, err := s.rt.workspaceReviews.Begin(sessionID, s.rt.WorkspaceRoot())
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "resumed": resumed, "pending_count": pending, "skipped_count": skipped}
}

func (s *AppService) ListWorkspaceReview(sessionID string) map[string]any {
	review, changes, err := s.rt.workspaceReviews.List(sessionID, s.rt.WorkspaceRoot())
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	if review == nil {
		return map[string]any{"success": true, "active": false, "changes": []workspace.ReviewChange{}}
	}
	return map[string]any{
		"success": true, "active": true, "root": review.Root,
		"created_at": review.CreatedAt.Format(time.RFC3339), "skipped_count": review.Skipped,
		"changes": changes,
	}
}

func (s *AppService) WorkspaceReviewFile(sessionID, relPath string) map[string]any {
	change, before, after, err := s.rt.workspaceReviews.Preview(sessionID, s.rt.WorkspaceRoot(), relPath)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "change": change, "original_content": before, "modified_content": after}
}

func (s *AppService) AcceptWorkspaceReviewFile(sessionID, relPath string) map[string]any {
	remaining, err := s.rt.workspaceReviews.Accept(sessionID, s.rt.WorkspaceRoot(), relPath)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error(), "remaining": remaining}
	}
	return map[string]any{"success": true, "remaining": remaining}
}

func (s *AppService) RejectWorkspaceReviewFile(sessionID, relPath string) map[string]any {
	remaining, err := s.rt.workspaceReviews.Reject(sessionID, s.rt.WorkspaceRoot(), relPath)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error(), "remaining": remaining}
	}
	return map[string]any{"success": true, "remaining": remaining}
}

func (s *AppService) AcceptAllWorkspaceReview(sessionID string) map[string]any {
	s.rt.workspaceReviews.AcceptAll(sessionID)
	return map[string]any{"success": true, "remaining": 0}
}

func (s *AppService) RejectAllWorkspaceReview(sessionID string) map[string]any {
	remaining, err := s.rt.workspaceReviews.RejectAll(sessionID, s.rt.WorkspaceRoot())
	if err != nil {
		return map[string]any{"success": false, "error": err.Error(), "remaining": remaining}
	}
	return map[string]any{"success": true, "remaining": remaining}
}
