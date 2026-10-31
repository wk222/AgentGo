//go:build !windows

package userhooks

// ansiToUTF8 has nothing to do off Windows: there is no system ANSI code page
// to fall back on, and scripts print UTF-8.
func ansiToUTF8(string) (string, bool) { return "", false }
