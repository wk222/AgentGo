package agent

import (
	"os"
	"strings"
)

// Matrix orchestration official path (when AUTO_FOLLOWUP is on):
//
//	1. AgentTool Supervisor (Eino adk.NewAgentTool) — default ON with AUTO_FOLLOWUP
//	2. Compose graph (plan → execute → summarize) — fallback when supervisor yields empty
//	3. legacy Generate (single-shot coordinator) — last resort
//
// Env overrides (see docs/matrix-orchestration.md):
//   - AGENTGO_MATRIX_AUTO_FOLLOWUP=1   master switch for capability-bus follow-up
//   - AGENTGO_MATRIX_SUPERVISOR=0|1    force off/on supervisor tier
//   - AGENTGO_MATRIX_COMPOSE=0|1       force off/on compose tier
//   - AGENTGO_MATRIX_EMIT_EVENTS=0|1   sub-agent internal events for trace/HITL

// MatrixAutoFollowupEnabled is the master switch for post-capability orchestration.
func MatrixAutoFollowupEnabled() bool {
	return strings.TrimSpace(os.Getenv("AGENTGO_MATRIX_AUTO_FOLLOWUP")) == "1"
}

// MatrixSupervisorEnabled uses AgentTool coordinator when AGENTGO_MATRIX_SUPERVISOR=1
// or when AUTO_FOLLOWUP=1 and AGENTGO_MATRIX_SUPERVISOR is not "0".
func MatrixSupervisorEnabled() bool {
	v := strings.TrimSpace(os.Getenv("AGENTGO_MATRIX_SUPERVISOR"))
	if v == "1" {
		return true
	}
	if v == "0" {
		return false
	}
	return MatrixAutoFollowupEnabled()
}

// MatrixComposeEnabled is on when AGENTGO_MATRIX_COMPOSE=1 or AUTO_FOLLOWUP=1 (unless COMPOSE=0).
func MatrixComposeEnabled() bool {
	if v := strings.TrimSpace(os.Getenv("AGENTGO_MATRIX_COMPOSE")); v == "1" {
		return true
	}
	if v := strings.TrimSpace(os.Getenv("AGENTGO_MATRIX_COMPOSE")); v == "0" {
		return false
	}
	return MatrixAutoFollowupEnabled()
}

// MatrixEmitInternalEvents controls sub-agent trace emission for matrix specialists.
func MatrixEmitInternalEvents() bool {
	if strings.TrimSpace(os.Getenv("AGENTGO_MATRIX_EMIT_EVENTS")) == "1" {
		return true
	}
	if v := strings.TrimSpace(os.Getenv("AGENTGO_MATRIX_EMIT_EVENTS")); v == "0" {
		return false
	}
	return MatrixAutoFollowupEnabled()
}

// MatrixTier names the orchestration stages in priority order.
type MatrixTier int

const (
	MatrixTierSupervisor MatrixTier = iota + 1
	MatrixTierCompose
	MatrixTierLegacy
)

// MatrixTierOrder returns the official fallback sequence.
func MatrixTierOrder() []MatrixTier {
	return []MatrixTier{MatrixTierSupervisor, MatrixTierCompose, MatrixTierLegacy}
}
