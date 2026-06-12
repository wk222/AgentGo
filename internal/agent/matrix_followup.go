package agent

import (
	"context"
	"strings"

	"agentgo/internal/capability"
)

// MatrixFollowupInput carries a capability-bus event through the official tier pipeline.
type MatrixFollowupInput struct {
	Event  capability.Event
	LLM    LLMSettings
	Prompt string
	// Tier runners — wired by bridge (consumer assembly).
	RunSupervisor func(ctx context.Context, prompt string) (content string, pending bool, err error)
	RunCompose    func(ctx context.Context, ev capability.Event) (summary, execErr string, ok bool)
	RunLegacy     func(ctx context.Context, prompt string) (content string, err error)
}

// MatrixFollowupResult is the first non-empty tier output.
type MatrixFollowupResult struct {
	Summary  string
	Error    string
	TierUsed MatrixTier
	Stopped  bool // true when supervisor paused for approval
}

// RunMatrixFollowup executes Supervisor → Compose → Legacy per MatrixTierOrder.
func RunMatrixFollowup(ctx context.Context, in MatrixFollowupInput) MatrixFollowupResult {
	if !MatrixAutoFollowupEnabled() {
		return MatrixFollowupResult{}
	}
	var out MatrixFollowupResult
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		prompt = MatrixEventPrompt(in.Event.Type, in.Event.Source, in.Event.SessionID, in.Event.Payload)
	}

	if MatrixSupervisorEnabled() && strings.TrimSpace(in.LLM.APIKey) != "" && in.RunSupervisor != nil {
		content, pending, err := in.RunSupervisor(ctx, prompt)
		if pending {
			return MatrixFollowupResult{TierUsed: MatrixTierSupervisor, Stopped: true, Summary: content}
		}
		if err != nil {
			out.Error = err.Error()
		} else if strings.TrimSpace(content) != "" {
			return MatrixFollowupResult{Summary: content, TierUsed: MatrixTierSupervisor}
		}
	}

	if MatrixComposeEnabled() && in.RunCompose != nil {
		summary, execErr, ok := in.RunCompose(ctx, in.Event)
		if ok && strings.TrimSpace(summary) != "" {
			return MatrixFollowupResult{Summary: summary, Error: execErr, TierUsed: MatrixTierCompose}
		}
		if execErr != "" && out.Error == "" {
			out.Error = execErr
		}
	}

	if in.RunLegacy != nil {
		content, err := in.RunLegacy(ctx, prompt)
		if err != nil && out.Error == "" {
			out.Error = err.Error()
		}
		if strings.TrimSpace(content) != "" {
			return MatrixFollowupResult{Summary: content, Error: out.Error, TierUsed: MatrixTierLegacy}
		}
	}

	out.TierUsed = 0
	return out
}
