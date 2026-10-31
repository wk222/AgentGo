//go:build windows

package shellcmd

import (
	"fmt"
	"log"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type jobProcessTree struct {
	cmd *exec.Cmd
	mu  sync.Mutex
	job windows.Handle // 0 once released, or when the command runs outside a job
}

// newJob creates the job a tree runs in, with the default Limits applied.
func newJob(opts TreeOptions) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create process job: %w", err)
	}
	limits := jobLimitInfo(DefaultLimits(), opts.Detachable)
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("configure process job: %w", err)
	}
	return job, nil
}

// Start suspended: assigning a running root to a job would leave a race where
// it could launch children before assignment. Resume only after ownership is set.
func startTree(cmd *exec.Cmd, opts TreeOptions) (ProcessTree, error) {
	job, err := newJob(opts)
	if err != nil {
		if !opts.BestEffort {
			return nil, err
		}
		log.Printf("[shellcmd] running without a process job: %v", err)
		job = 0
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}
	tree := &jobProcessTree{cmd: cmd, job: job}
	// Install before Start launches its context watcher: cancel the owned job.
	if cmd.Cancel != nil {
		cmd.Cancel = func() error { tree.Kill(); return nil }
	}
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = 2 * time.Second
	}
	if job == 0 {
		if err = cmd.Start(); err != nil {
			return nil, err
		}
		return tree, nil
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err = cmd.Start(); err != nil {
		tree.release()
		return nil, err
	}
	pid := uint32(cmd.Process.Pid)
	if err = assignToJob(job, pid); err != nil {
		if !opts.BestEffort {
			return tree.abort(err)
		}
		log.Printf("[shellcmd] pid %d runs without a process job: %v", pid, err)
		tree.release()
	}
	if err = resumeOwnedInitialThread(pid); err != nil {
		return tree.abort(err)
	}
	return tree, nil
}

func assignToJob(job windows.Handle, pid uint32) error {
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return fmt.Errorf("open process for job: %w", err)
	}
	err = windows.AssignProcessToJobObject(job, handle)
	windows.CloseHandle(handle)
	if err != nil {
		return fmt.Errorf("assign process job: %w", err)
	}
	return nil
}

func resumeOwnedInitialThread(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("snapshot suspended thread: %w", err)
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if openErr != nil {
			return fmt.Errorf("open initial thread: %w", openErr)
		}
		_, resumeErr := windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		if resumeErr != nil {
			return fmt.Errorf("resume initial thread: %w", resumeErr)
		}
		return nil
	}
	return fmt.Errorf("initial thread for suspended process %d not found: %w", pid, err)
}

// abort ends a command that was started suspended but cannot be let go.
func (t *jobProcessTree) abort(cause error) (ProcessTree, error) {
	_ = t.cmd.Process.Kill()
	_ = t.cmd.Wait()
	t.release()
	return nil, cause
}

// release lets go of the job handle. Without Detachable that ends whatever is
// still running in the job (KILL_ON_JOB_CLOSE); with it the survivors stay.
func (t *jobProcessTree) release() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job != 0 {
		windows.CloseHandle(t.job)
		t.job = 0
	}
}

func (t *jobProcessTree) Wait() error {
	err := t.cmd.Wait()
	t.release()
	return err
}

func (t *jobProcessTree) Kill() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job != 0 {
		_ = windows.TerminateJobObject(t.job, 1)
	}
	_ = t.cmd.Process.Kill()
}
