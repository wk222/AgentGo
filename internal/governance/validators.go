package governance

import (
	"context"
	"fmt"
	"strings"
)

// ToolValidator is one step in the policy pipeline (PyBot tool_policy_pipeline subset).
type ToolValidator interface {
	Validate(ctx context.Context, toolName, argumentsJSON string, risk RiskLevel) error
}

// ValidatorChain runs validators in order; first error stops the chain.
type ValidatorChain struct {
	validators []ToolValidator
}

func NewValidatorChain(v ...ToolValidator) *ValidatorChain {
	return &ValidatorChain{validators: v}
}

func (c *ValidatorChain) Validate(ctx context.Context, toolName, args string, risk RiskLevel) error {
	if c == nil {
		return nil
	}
	for _, v := range c.validators {
		if v == nil {
			continue
		}
		if err := v.Validate(ctx, toolName, args, risk); err != nil {
			return err
		}
	}
	return nil
}

// BlockedToolValidator rejects policy.BlockedTools.
type BlockedToolValidator struct {
	Blocked map[string]bool
}

func (v BlockedToolValidator) Validate(_ context.Context, toolName, _ string, _ RiskLevel) error {
	if v.Blocked != nil && v.Blocked[toolName] {
		return fmt.Errorf("governance: tool %q is blocked", toolName)
	}
	return nil
}

// ArgumentSizeValidator caps JSON argument payload size.
type ArgumentSizeValidator struct {
	MaxBytes int
}

func (v ArgumentSizeValidator) Validate(_ context.Context, toolName, args string, _ RiskLevel) error {
	max := v.MaxBytes
	if max <= 0 {
		max = 256 * 1024
	}
	if len(args) > max {
		return fmt.Errorf("governance: tool %q arguments exceed %d bytes", toolName, max)
	}
	return nil
}

// DelegationDepthValidator blocks invoke_subagent when args mention excessive depth (hint).
type DelegationDepthValidator struct{}

func (DelegationDepthValidator) Validate(_ context.Context, toolName, args string, _ RiskLevel) error {
	if toolName != "invoke_subagent" {
		return nil
	}
	if strings.Contains(strings.ToLower(args), "depth_exceeded") {
		return fmt.Errorf("governance: subagent delegation refused")
	}
	return nil
}

// DefaultValidatorChain returns the standard pipeline for GovernanceMiddleware.
func DefaultValidatorChain(policy Policy) *ValidatorChain {
	return NewValidatorChain(
		BlockedToolValidator{Blocked: policy.BlockedTools},
		ArgumentSizeValidator{MaxBytes: 512 * 1024},
		DelegationDepthValidator{},
	)
}
