package cli

import "os"

// supportsColor decides whether f should receive ANSI styling.
func supportsColor(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || !isTerminal(f) {
		return false
	}
	return enableVT(f)
}
