//go:build !windows

package shellcmd

// PhysicalMemoryBytes reports the installed physical memory. Only Windows
// enforces Limits, so elsewhere it reports 0 ("unknown") and the automatic
// memory cap stays off.
func PhysicalMemoryBytes() uint64 { return 0 }
