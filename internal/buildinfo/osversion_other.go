//go:build !windows

package buildinfo

func osVersion() string { return "" }
