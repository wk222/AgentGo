//go:build windows

package shellcmd

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestJobLimitInfoFlags(t *testing.T) {
	info := jobLimitInfo(Limits{MemoryBytes: 512 << 20, MaxProcesses: 8}, false)
	basic := info.BasicLimitInformation
	want := uint32(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION |
		windows.JOB_OBJECT_LIMIT_JOB_MEMORY | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS)
	if basic.LimitFlags != want || info.JobMemoryLimit != 512<<20 || basic.ActiveProcessLimit != 8 {
		t.Fatalf("flags %#x memory %d processes %d", basic.LimitFlags, info.JobMemoryLimit, basic.ActiveProcessLimit)
	}
	bare := jobLimitInfo(Limits{}, true).BasicLimitInformation.LimitFlags
	if bare != windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION {
		t.Fatalf("an unlimited detachable job must only suppress crash dialogs: %#x", bare)
	}
}

func TestPhysicalMemoryIsReadable(t *testing.T) {
	if phys := PhysicalMemoryBytes(); phys < 256<<20 {
		t.Fatalf("physical memory read as %d bytes", phys)
	}
}

// What the job was configured with is what the system reports for it.
func TestRunningTreeCarriesTheDefaultLimits(t *testing.T) {
	setLimits(t, Limits{MemoryBytes: 512 << 20, MaxProcesses: 16})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tree, err := StartTree(helperCmd(ctx, t.TempDir(), "child"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { tree.Kill(); tree.Wait() }()
	job := tree.(*jobProcessTree)
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	job.mu.Lock()
	err = windows.QueryInformationJobObject(job.job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
	job.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	flags := info.BasicLimitInformation.LimitFlags
	for name, bit := range map[string]uint32{
		"kill on close":   windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		"no crash dialog": windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION,
		"memory cap":      windows.JOB_OBJECT_LIMIT_JOB_MEMORY,
		"process cap":     windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS,
	} {
		if flags&bit == 0 {
			t.Errorf("the job lacks %s (flags %#x)", name, flags)
		}
	}
	if info.JobMemoryLimit != 512<<20 || info.BasicLimitInformation.ActiveProcessLimit != 16 {
		t.Fatalf("memory %d processes %d", info.JobMemoryLimit, info.BasicLimitInformation.ActiveProcessLimit)
	}
}

// The system, not just the configuration, refuses the allocation: a process that
// wants 2 GiB under a 256 MiB cap does not get there.
//
// How the helper ends differs by build. A 64-bit one dies at once with "out of
// memory". A 32-bit one under WOW64 (the 386 test binary) has used up the job's
// commit limit and often cannot finish exiting, because exiting needs memory too:
// it crashes, or just sits there. The cap has done its job either way, so the
// helper gets a deadline and the verdict rests on how far it got.
func TestMemoryLimitStopsARunawayProcess(t *testing.T) {
	setLimits(t, Limits{MemoryBytes: 256 << 20})
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, err := Run(helperCmd(ctx, dir, "alloc"), TreeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "progress.txt"))
	reached, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if code == 0 || reached >= 1024 {
		t.Fatalf("status %d after taking %d MiB under a 256 MiB cap", code, reached)
	}
	if reached < 32 {
		t.Fatalf("the helper stopped after %d MiB: the test did not measure the cap", reached)
	}
}

func TestProcessLimitStopsAForkBomb(t *testing.T) {
	setLimits(t, Limits{MaxProcesses: 4})
	dir := t.TempDir()
	begin := time.Now()
	if _, err := Run(helperCmd(context.Background(), dir, "spawn"), TreeOptions{}); err != nil {
		t.Fatal(err)
	}
	alive, _ := filepath.Glob(filepath.Join(dir, "nap-*.txt"))
	if len(alive) == 0 || len(alive) > 3 {
		t.Fatalf("%d of 8 helpers ran under a cap of 4 processes (root included) after %v", len(alive), time.Since(begin))
	}
}
