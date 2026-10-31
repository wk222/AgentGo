//go:build !windows

package main

// showFatalDialog is a no-op where the process has a terminal to print to.
func showFatalDialog(title, text string) {}
