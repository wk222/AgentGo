package bridge

import (
	"context"
	"fmt"
	"strings"

	"agentgo/internal/governance"
)

// ApproveAndRemember approves a pending tool approval and remembers the
// decision for the rest of this conversation, so the same kind of call is not
// asked about again. scope is "call" (this exact call) or "tool" (every call of
// the tool); tools that are too broad to allow wholesale refuse "tool".
func (s *AppService) ApproveAndRemember(approvalID, scope, note string) map[string]any {
	fail := func(msg string) map[string]any { return map[string]any{"success": false, "error": msg} }

	sc, err := governance.ParseRememberScope(scope)
	if err != nil {
		return fail(err.Error())
	}
	q := s.rt.Approvals()
	if q == nil {
		return fail("approval queue unavailable")
	}
	ctx := context.Background()
	req, err := q.GetRequest(ctx, approvalID)
	if err != nil || req == nil {
		return fail(fmt.Sprintf("approval %q not found", approvalID))
	}
	meta := req.Metadata
	toolName, _ := meta["tool_name"].(string)
	args, _ := meta["arguments"].(string)
	sessionID, _ := meta["session_id"].(string)
	if strings.TrimSpace(toolName) == "" {
		return fail("this approval is not a tool call, so there is nothing to remember")
	}
	if err := governance.RememberApproval(sessionID, toolName, args, sc); err != nil {
		return fail(err.Error())
	}
	out := s.resolveApproval(ctx, approvalID, true, note, "desktop_user", "")
	if out != nil {
		out["remembered"] = string(sc)
	}
	return out
}

// ForgetRememberedApprovals drops every remembered approval of a session.
func (s *AppService) ForgetRememberedApprovals(sessionID string) map[string]any {
	governance.ForgetRemembered(sessionID)
	return map[string]any{"success": true}
}

// ListRememberedTools lists the tools a session allows without asking.
func (s *AppService) ListRememberedTools(sessionID string) []string {
	return governance.RememberedTools(sessionID)
}
