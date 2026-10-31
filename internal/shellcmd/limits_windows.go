//go:build windows

package shellcmd

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGlobalMemoryStatusEx = kernel32DLL.NewProc("GlobalMemoryStatusEx")

// memoryStatusEx is MEMORYSTATUSEX.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// PhysicalMemoryBytes reports the installed physical memory, or 0 when it
// cannot be read.
func PhysicalMemoryBytes() uint64 {
	status := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if ok, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status))); ok == 0 {
		return 0
	}
	return status.TotalPhys
}

// jobLimitInfo is what a job is configured with. A crash inside the job never
// raises Windows' error-reporting dialog, which would sit unseen on a headless
// machine and hold the whole command open. Unless the tree may be detached, closing
// the job also ends whatever still runs in it.
func jobLimitInfo(l Limits, detachable bool) windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	flags := uint32(windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION)
	if !detachable {
		flags |= windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	}
	if l.MemoryBytes > 0 {
		flags |= windows.JOB_OBJECT_LIMIT_JOB_MEMORY
		info.JobMemoryLimit = uintptr(min(l.MemoryBytes, uint64(^uintptr(0))))
	}
	if l.MaxProcesses > 0 {
		flags |= windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
		info.BasicLimitInformation.ActiveProcessLimit = l.MaxProcesses
	}
	info.BasicLimitInformation.LimitFlags = flags
	return info
}
