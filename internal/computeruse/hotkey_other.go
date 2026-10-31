//go:build !windows

package computeruse

// StopHotkeyLabel is empty off Windows: there is no hotkey.
const StopHotkeyLabel = ""

// StartStopHotkey is a no-op off Windows.
func StartStopHotkey(*Controller) (func(), error) { return func() {}, nil }
