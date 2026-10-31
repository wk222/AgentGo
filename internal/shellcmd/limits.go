package shellcmd

import "sync/atomic"

// Limits bounds what the processes of one command tree may use together. A zero
// field means no limit. Windows enforces them through the job that owns the
// tree; elsewhere they are accepted and ignored.
type Limits struct {
	// MemoryBytes caps the memory the tree has committed. A process that asks
	// for more than is left fails that allocation; the tree is not killed, so
	// what happens next is up to the program (most stop with an out-of-memory error).
	MemoryBytes uint64
	// MaxProcesses caps how many processes of the tree may be alive at once.
	// One that would be the next is ended as it starts, which keeps a runaway
	// "start myself again" loop from taking the machine.
	MaxProcesses uint32
}

var defaultLimits atomic.Pointer[Limits]

// SetDefaultLimits sets the limits every tree started afterwards gets. The
// runtime calls it once at boot from the user's settings; until then trees are
// unlimited.
func SetDefaultLimits(l Limits) { defaultLimits.Store(&l) }

// DefaultLimits returns the limits SetDefaultLimits last set.
func DefaultLimits() Limits {
	if l := defaultLimits.Load(); l != nil {
		return *l
	}
	return Limits{}
}

// autoMemoryPercent is the share of physical memory one command tree may commit
// when the user did not choose a size: far above any normal build or script,
// well short of the point where the machine starts to thrash.
const autoMemoryPercent = 70

// LimitsFromSettings turns the user's settings into Limits. memMB is the memory
// cap in MiB: negative picks autoMemoryPercent of the physical memory (no cap
// when it cannot be read), zero turns the cap off. maxProcs of zero or less
// turns the process cap off.
func LimitsFromSettings(memMB, maxProcs int) Limits {
	var l Limits
	switch {
	case memMB > 0:
		l.MemoryBytes = uint64(memMB) << 20
	case memMB < 0:
		l.MemoryBytes = PhysicalMemoryBytes() / 100 * autoMemoryPercent
	}
	if maxProcs > 0 {
		l.MaxProcesses = uint32(maxProcs)
	}
	return l
}
