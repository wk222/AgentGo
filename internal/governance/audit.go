package governance

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AuditEntry tracks dynamic tool invocation controls, HITL requests, approvals, bypasses, and denials.
type AuditEntry struct {
	ID             string            `json:"id"`
	Timestamp      int64             `json:"timestamp"`
	Channel        string            `json:"channel"` // agent, workflow, matrix, admin, inner_app
	SessionID      string            `json:"session_id"`
	Action         string            `json:"action"` // tool_invoke, approval_create, approval_approve, policy_deny, etc.
	ToolName       string            `json:"tool_name,omitempty"`
	Arguments      string            `json:"arguments,omitempty"`
	Result         string            `json:"result,omitempty"` // success, denied, error, interrupted
	RiskLevel      string            `json:"risk_level,omitempty"`
	PolicySnapshot map[string]string `json:"policy_snapshot,omitempty"`
	UserID         string            `json:"user_id,omitempty"`
	Explanation    string            `json:"explanation,omitempty"`
}

// RecordAudit persists a new audit log entry into the database.
func (q *ApprovalQueue) RecordAudit(ctx context.Context, entry AuditEntry) error {
	if q == nil || q.db == nil {
		return nil
	}
	if entry.ID == "" {
		entry.ID = uuid.NewString()
	}
	if entry.Timestamp == 0 {
		entry.Timestamp = time.Now().Unix()
	}
	snapBytes, _ := json.Marshal(entry.PolicySnapshot)
	_, err := q.db.ExecContext(ctx, `
		INSERT INTO governance_audit_log (
			id, timestamp, channel, session_id, action, tool_name, arguments, result, risk_level, policy_snapshot, user_id, explanation
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
	`, entry.ID, entry.Timestamp, entry.Channel, entry.SessionID, entry.Action, entry.ToolName, entry.Arguments, entry.Result, entry.RiskLevel, string(snapBytes), entry.UserID, entry.Explanation)
	return err
}

// QueryAudit retrieves audit logs filtered by SessionID ordered by timestamp descending.
func (q *ApprovalQueue) QueryAudit(ctx context.Context, sessionID string) ([]AuditEntry, error) {
	if q == nil || q.db == nil {
		return nil, nil
	}
	rows, err := q.db.QueryContext(ctx, `
		SELECT id, timestamp, channel, session_id, action, tool_name, arguments, result, risk_level, policy_snapshot, user_id, explanation
		FROM governance_audit_log WHERE session_id = ? ORDER BY timestamp DESC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AuditEntry
	for rows.Next() {
		var ae AuditEntry
		var toolName, arguments, result, riskLevel, policySnap, userID, explanation []byte
		err := rows.Scan(
			&ae.ID, &ae.Timestamp, &ae.Channel, &ae.SessionID, &ae.Action,
			&toolName, &arguments, &result, &riskLevel, &policySnap, &userID, &explanation,
		)
		if err != nil {
			return nil, err
		}
		if len(toolName) > 0 {
			ae.ToolName = string(toolName)
		}
		if len(arguments) > 0 {
			ae.Arguments = string(arguments)
		}
		if len(result) > 0 {
			ae.Result = string(result)
		}
		if len(riskLevel) > 0 {
			ae.RiskLevel = string(riskLevel)
		}
		if len(policySnap) > 0 {
			_ = json.Unmarshal(policySnap, &ae.PolicySnapshot)
		}
		if len(userID) > 0 {
			ae.UserID = string(userID)
		}
		if len(explanation) > 0 {
			ae.Explanation = string(explanation)
		}
		out = append(out, ae)
	}
	return out, rows.Err()
}
