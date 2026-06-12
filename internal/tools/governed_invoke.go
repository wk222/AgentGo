package tools

import (
	"context"

	"agentgo/internal/governance"
)

// GovernedInvokeJSON runs InvokeJSON through GovernanceMiddleware (ADK-equivalent policy + interrupt).
func GovernedInvokeJSON(ctx context.Context, reg *Registry, gov *governance.GovernanceMiddleware, toolName, argsJSON string) (string, error) {
	if reg == nil {
		return "", ErrRegistryUnavailable
	}
	if gov == nil {
		return reg.InvokeJSON(ctx, toolName, argsJSON)
	}
	return gov.InvokeWithPolicy(ctx, toolName, argsJSON, func(ctx context.Context, args string) (string, error) {
		return reg.InvokeJSON(ctx, toolName, args)
	})
}

// ErrRegistryUnavailable is returned when the tool registry is nil.
var ErrRegistryUnavailable = errRegistryUnavailable("tool registry unavailable")

type errRegistryUnavailable string

func (e errRegistryUnavailable) Error() string { return string(e) }
