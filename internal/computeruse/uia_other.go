//go:build !windows

package computeruse

// NewAccessibility returns nil off Windows: there is no accessibility channel
// and the Controller falls back to pixel actions only.
func NewAccessibility() Accessibility { return nil }
