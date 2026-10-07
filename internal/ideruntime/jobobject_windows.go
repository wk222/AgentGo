//go:build windows

package ideruntime

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type jobObjectManager struct {
	mu     sync.Mutex
	handle windows.Handle
}

func newJobObjectManager() (*jobObjectManager, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateJobObject error: %w", err)
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}

	_, err = windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("SetInformationJobObject error: %w", err)
	}

	return &jobObjectManager{handle: h}, nil
}

func (j *jobObjectManager) AssignProcess(pid int) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.handle == 0 {
		return fmt.Errorf("job object closed")
	}

	hProc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("OpenProcess pid=%d: %w", pid, err)
	}
	defer windows.CloseHandle(hProc)

	if err := windows.AssignProcessToJobObject(j.handle, hProc); err != nil {
		return fmt.Errorf("AssignProcessToJobObject pid=%d: %w", pid, err)
	}
	return nil
}

func (j *jobObjectManager) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.handle != 0 {
		err := windows.CloseHandle(j.handle)
		j.handle = 0
		return err
	}
	return nil
}
