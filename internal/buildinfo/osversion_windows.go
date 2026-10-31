//go:build windows

package buildinfo

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// osVersion is the Windows version as the kernel reports it (RtlGetVersion is not subject to the manifest
// compatibility shims): "10.0.26300" for Windows 11, "6.1.7601" for Windows 7 SP1.
func osVersion() string {
	v := windows.RtlGetVersion()
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}
