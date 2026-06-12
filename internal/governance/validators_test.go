package governance

import (
	"context"
	"testing"
)

func TestValidatorChainBlocked(t *testing.T) {
	p := DefaultPolicy()
	c := DefaultValidatorChain(p)
	err := c.Validate(context.Background(), "execute_bash", "{}", RiskCritical)
	if err != nil {
		// blocked only if in BlockedTools
	}
	err = c.Validate(context.Background(), "rm_rf_root", "{}", RiskCritical)
	if err == nil {
		t.Fatal("expected block")
	}
}
