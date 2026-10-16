package bridge

import "agentgo/internal/config"

// GetConfigSnapshot lists every declared AGENTGO_* setting with its effective value,
// default and documentation. Read-only: values come from the process environment and
// secrets are masked by config.Snapshot.
func (s *AppService) GetConfigSnapshot() []config.Entry {
	return config.Snapshot()
}
