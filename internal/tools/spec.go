package tools

import "agentgo/internal/governance"

// Spec is the one declaration a plugin makes about a tool it contributes.
//
// Before Spec, the same tool had to be described in separate places that could
// drift apart: always-visible (RegisterStaticTool), file-changing and source
// (governance.RegisterToolTraits), and risk (a name list in the governance
// table). Declare writes all of them from this single record, and withdraws them
// together, so a tool is either fully described or not at all.
type Spec struct {
	Name     string
	Source   string               // contributing plugin, for audit
	Static   bool                 // always visible to the model (no tool_search needed)
	Mutating bool                 // changes files in the workspace
	Risk     governance.RiskLevel // baseline risk; "" = LOW
}

// Declare publishes spec and returns a func that withdraws everything it
// published. Call it before the tool is added to the registry so the capability
// bus grant created by the contribution already sees the declared risk.
func Declare(s Spec) (dispose func()) {
	var disposers []func()
	disposers = append(disposers, governance.RegisterToolTraits(s.Name, governance.ToolTraits{
		Source: s.Source, Mutating: s.Mutating, Risk: s.Risk,
	}))
	if s.Static {
		disposers = append(disposers, RegisterStaticTool(s.Name, s.Mutating))
	}
	return func() {
		for i := len(disposers) - 1; i >= 0; i-- {
			disposers[i]()
		}
	}
}
