//go:build !windows

package cli

import "os"

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func enableVT(f *os.File) bool { return isTerminal(f) }
