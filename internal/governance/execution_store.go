package governance

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
)

const executionSchema = `
CREATE TABLE IF NOT EXISTS governance_daily_budget (
 day TEXT PRIMARY KEY, units INTEGER NOT NULL DEFAULT 0 CHECK(units>=0));
CREATE TABLE IF NOT EXISTS governance_tool_starts (
 id TEXT PRIMARY KEY, execution_id TEXT NOT NULL, tool_name TEXT NOT NULL,
 started_at INTEGER NOT NULL, day TEXT NOT NULL, units INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS idx_governance_starts_scope
 ON governance_tool_starts(execution_id,tool_name,started_at);
`

// Keep the existing ten-minute call window, now shared across middleware
// constructions and persisted across checkpoint/process restart.
const executionWindow = 10 * time.Minute

func (q *ApprovalQueue) recentCalls(ctx context.Context, root, name string) (int, error) {
	if q == nil {
		return 0, fmt.Errorf("governance: execution store unavailable")
	}
	var n int
	err := q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM governance_tool_starts WHERE execution_id=? AND tool_name=? AND started_at>=?`, root, name, time.Now().Add(-executionWindow).Unix()).Scan(&n)
	return n, err
}

// admitExecution atomically claims an approval, a rate-limit slot and budget.
// No human wait or hook is held inside this transaction. Units are policy
// credits (100 per critical invocation, 1 per low-risk invocation), not money.
func (q *ApprovalQueue) admitExecution(ctx context.Context, root, name string, risk RiskLevel, p Policy, approvalID, fingerprint string) (string, error) {
	if q == nil {
		return "", fmt.Errorf("governance: execution store unavailable")
	}
	limit := p.MaxDailyBudget
	if limit == 0 {
		limit = DefaultPolicy().MaxDailyBudget
	} // bare Policy{} uses defaults
	if math.IsNaN(limit) || math.IsInf(limit, 0) || limit < 0 || limit > float64(math.MaxInt64/100) {
		return "", fmt.Errorf("governance: invalid daily budget")
	}
	maxUnits := int64(math.Floor(limit*100 + 1e-8))
	units := int64(0)
	if risk == RiskCritical {
		units = 100
	} else if risk == RiskLow {
		units = 1
	}
	day := time.Now().Format("2006-01-02")
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	// Acquire SQLite's writer lock before reading counts, including when two
	// queue handles/processes share the database.
	if _, err = tx.ExecContext(ctx, `INSERT INTO governance_daily_budget(day,units) VALUES (?,0) ON CONFLICT(day) DO NOTHING`, day); err != nil {
		return "", err
	}
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM governance_tool_starts WHERE execution_id=? AND tool_name=? AND started_at>=?`, root, name, time.Now().Add(-executionWindow).Unix()).Scan(&n); err != nil {
		return "", err
	}
	ctrl := p.NormalizeControl()
	if ctrl.MaxCallsPerTool > 0 && n >= ctrl.MaxCallsPerTool {
		return "", fmt.Errorf("governance: tool call rate limit reached")
	}
	if ctrl.StuckLoopKillThreshold > 0 && n >= ctrl.StuckLoopKillThreshold {
		return "", fmt.Errorf("governance: stuck tool loop blocked")
	}
	res, err := tx.ExecContext(ctx, `UPDATE governance_daily_budget SET units=units+? WHERE day=? AND units<=?-?`, units, day, maxUnits, units)
	if err != nil {
		return "", err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if changed != 1 {
		return "", fmt.Errorf("governance: daily budget exceeded")
	}
	if approvalID != "" {
		if err = consumeBoundApproval(ctx, tx, approvalID, fingerprint, p.WorkspaceRoot); err != nil {
			return "", err
		}
	}
	id := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO governance_tool_starts(id,execution_id,tool_name,started_at,day,units) VALUES (?,?,?,?,?,?)`, id, root, name, time.Now().Unix(), day, units); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

// A framework pause or cancellation before endpoint entry is not a completed
// attempt. Release credits/call slot; an approval claim never becomes reusable.
func (q *ApprovalQueue) releaseExecutionReservation(ctx context.Context, id string) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var day string
	var units int64
	if err = tx.QueryRowContext(ctx, `DELETE FROM governance_tool_starts WHERE id=? RETURNING day,units`, id).Scan(&day, &units); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE governance_daily_budget SET units=units-? WHERE day=? AND units>=?`, units, day, units); err != nil {
		return err
	}
	return tx.Commit()
}
