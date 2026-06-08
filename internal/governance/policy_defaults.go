package governance

// DefaultPolicy returns balanced preset + static risk table (PyBot agent_control balanced).
func DefaultPolicy() Policy {
	p := BuildPolicy(string(ControlBalanced), "")
	p.BlockedTools["rm_rf_root"] = true
	return p
}
