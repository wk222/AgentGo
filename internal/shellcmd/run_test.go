package shellcmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRunHelper is the program the tests below start. It does nothing unless
// AGENTGO_RUN_HELPER is set, and then does what the word after "--" says.
func TestRunHelper(t *testing.T) {
	if os.Getenv("AGENTGO_RUN_HELPER") != "1" {
		return
	}
	var mode, arg string
	for i, a := range os.Args {
		if a == "--" && i+1 < len(os.Args) {
			mode = os.Args[i+1]
			if i+2 < len(os.Args) {
				arg = os.Args[i+2]
			}
		}
	}
	switch mode {
	case "child": // starts at once, finishes three seconds later
		os.WriteFile("started.txt", []byte("started"), 0o600)
		time.Sleep(3 * time.Second)
		os.WriteFile("marker.txt", []byte("survived"), 0o600)
		os.Exit(0)
	case "parent": // starts a child and leaves without it
		startHelperChild()
		os.Exit(0)
	case "holder": // starts a child and stays until it is done
		startHelperChild().Wait()
		os.Exit(0)
	case "alloc": // takes memory until it has 2 GiB or the system refuses
		var keep [][]byte
		for i := 1; i <= 64; i++ {
			block := make([]byte, 32<<20)
			for j := 0; j < len(block); j += 4096 {
				block[j] = 1
			}
			keep = append(keep, block)
			os.WriteFile("progress.txt", []byte(strconv.Itoa(i*32)), 0o600)
		}
		runtime.KeepAlive(keep)
		os.Exit(0)
	case "spawn": // starts eight processes that each stay for a while
		for i := 0; i < 8; i++ {
			c := exec.Command(os.Args[0], "-test.run=^TestRunHelper$", "--", "nap", strconv.Itoa(i))
			c.Stdout, c.Stderr = os.Stdout, os.Stderr
			_ = c.Start() // beyond the limit this fails or the process dies at once
		}
		time.Sleep(1500 * time.Millisecond)
		os.Exit(0)
	case "nap":
		os.WriteFile("nap-"+arg+".txt", []byte("alive"), 0o600)
		time.Sleep(1500 * time.Millisecond)
		os.Exit(0)
	}
	os.Exit(9)
}

// startHelperChild starts a "child" helper and returns once it is running.
func startHelperChild() *exec.Cmd {
	c := exec.Command(os.Args[0], "-test.run=^TestRunHelper$", "--", "child")
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	if err := c.Start(); err != nil {
		os.Exit(10)
	}
	if !waitFor("started.txt", 10*time.Second) {
		os.Exit(11)
	}
	return c
}

func helperCmd(ctx context.Context, dir, mode string, extra ...string) *exec.Cmd {
	args := append([]string{"-test.run=^TestRunHelper$", "--", mode}, extra...)
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "AGENTGO_RUN_HELPER=1")
	cmd.WaitDelay = 300 * time.Millisecond
	return cmd
}

func setLimits(t *testing.T, l Limits) {
	t.Helper()
	prev := DefaultLimits()
	SetDefaultLimits(l)
	t.Cleanup(func() { SetDefaultLimits(prev) })
}

func TestRunReportsTheExitStatus(t *testing.T) {
	for _, want := range []int{0, 3} {
		code, err := Run(Command(context.Background(), fmt.Sprintf("exit %d", want)), TreeOptions{})
		if err != nil || code != want {
			t.Fatalf("exit %d: got status %d, err %v", want, code, err)
		}
	}
}

func TestRunReportsACommandThatCannotStart(t *testing.T) {
	code, err := Run(exec.Command("agentgo-no-such-program-xyz"), TreeOptions{})
	if err == nil || code != -1 {
		t.Fatalf("a missing program gave status %d, err %v", code, err)
	}
}

func TestRunEndsTheWholeTreeAtTheDeadline(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	begin := time.Now()
	code, err := Run(helperCmd(ctx, dir, "holder"), TreeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(begin); took > 3500*time.Millisecond {
		t.Fatalf("the command ran %v past a 2s deadline", took)
	}
	if ctx.Err() == nil || code == 0 {
		t.Fatalf("stopped command: status %d, context %v", code, ctx.Err())
	}
	if _, err := os.Stat(filepath.Join(dir, "started.txt")); err != nil {
		t.Fatal("the child never started; the test cannot tell anything")
	}
	if waitFor(filepath.Join(dir, "marker.txt"), 3500*time.Millisecond) {
		t.Fatal("a process of the stopped command survived")
	}
}

func TestRunDetachableLetsWhatTheCommandLeftKeepRunning(t *testing.T) {
	dir := t.TempDir()
	code, err := Run(helperCmd(context.Background(), dir, "parent"), TreeOptions{Detachable: true})
	if err != nil || code != 0 {
		t.Fatalf("status %d, err %v", code, err)
	}
	if !waitFor(filepath.Join(dir, "marker.txt"), 8*time.Second) {
		t.Fatal("the child was ended together with its root although the tree is detachable")
	}
}

func TestRunDetachableStillEndsEverythingOnCancel(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := Run(helperCmd(ctx, dir, "holder"), TreeOptions{Detachable: true}); err != nil {
		t.Fatal(err)
	}
	if waitFor(filepath.Join(dir, "marker.txt"), 3500*time.Millisecond) {
		t.Fatal("a detachable tree survived its cancelled context")
	}
}

// An orphan that keeps the output pipe open must not turn a finished command
// into a failure or hold the caller up.
func TestRunIgnoresAnOrphanHoldingTheOutputPipe(t *testing.T) {
	dir := t.TempDir()
	cmd := helperCmd(context.Background(), dir, "parent")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	begin := time.Now()
	code, err := Run(cmd, TreeOptions{Detachable: true})
	if err != nil || code != 0 {
		t.Fatalf("status %d, err %v, output %q", code, err, out.String())
	}
	if took := time.Since(begin); took > 2500*time.Millisecond {
		t.Fatalf("Run waited %v for an orphan", took)
	}
	// The orphan still has the directory as its working directory; let it end
	// before the test's cleanup removes it.
	if !waitFor(filepath.Join(dir, "marker.txt"), 8*time.Second) {
		t.Fatal("the orphan never finished")
	}
	time.Sleep(300 * time.Millisecond)
}

func TestLimitsFromSettings(t *testing.T) {
	if got := LimitsFromSettings(512, 64); got != (Limits{MemoryBytes: 512 << 20, MaxProcesses: 64}) {
		t.Fatalf("explicit settings: %+v", got)
	}
	if got := LimitsFromSettings(0, 0); got != (Limits{}) {
		t.Fatalf("zero turns both caps off: %+v", got)
	}
	if got := LimitsFromSettings(0, -5); got.MaxProcesses != 0 {
		t.Fatalf("a negative process cap must mean none: %+v", got)
	}
	auto := LimitsFromSettings(-1, 0)
	if phys := PhysicalMemoryBytes(); phys > 0 {
		if want := phys / 100 * autoMemoryPercent; auto.MemoryBytes != want {
			t.Fatalf("automatic cap %d, want %d of %d bytes of memory", auto.MemoryBytes, want, phys)
		}
	} else if auto.MemoryBytes != 0 {
		t.Fatalf("memory unknown but a cap was chosen: %+v", auto)
	}
}

func TestDefaultLimitsAreWhatWasSet(t *testing.T) {
	want := Limits{MemoryBytes: 1 << 30, MaxProcesses: 12}
	setLimits(t, want)
	if got := DefaultLimits(); got != want {
		t.Fatalf("got %+v", got)
	}
}

func TestCaptureKeepsAShortOutputWhole(t *testing.T) {
	c := NewCapture(100)
	c.Write([]byte("hello "))
	c.Write([]byte("world"))
	if got := c.String(); got != "hello world" || c.Dropped() != 0 {
		t.Fatalf("got %q, dropped %d", got, c.Dropped())
	}
}

func TestCaptureKeepsTheStartAndTheEndOfALongOutput(t *testing.T) {
	c := NewCapture(100)
	stream := strings.Repeat("a", 400) + strings.Repeat("b", 400) + strings.Repeat("c", 200)
	for i := 0; i < len(stream); i += 37 { // uneven chunks, as a pipe delivers them
		end := min(i+37, len(stream))
		if n, err := c.Write([]byte(stream[i:end])); n != end-i || err != nil {
			t.Fatalf("short write: %d, %v", n, err)
		}
	}
	got := c.String()
	if !strings.HasPrefix(got, strings.Repeat("a", 50)+"\n[... 900 bytes of output dropped ...]\n") {
		t.Fatalf("start or marker wrong: %q", got)
	}
	if !strings.HasSuffix(got, strings.Repeat("c", 50)) {
		t.Fatalf("the end was not kept: %q", got)
	}
	if c.Dropped() != 900 {
		t.Fatalf("dropped %d, want 900", c.Dropped())
	}
}

func TestCaptureAcceptsAHugeWriteWithoutKeepingIt(t *testing.T) {
	c := NewCapture(1000)
	if n, err := c.Write(bytes.Repeat([]byte("x"), 1<<20)); n != 1<<20 || err != nil {
		t.Fatalf("short write: %d, %v", n, err)
	}
	if kept := len(c.head) + len(c.tail); kept > 1500 {
		t.Fatalf("kept %d bytes of a 1 MiB write", kept)
	}
	if want := int64(1<<20 - 1000); c.Dropped() != want {
		t.Fatalf("dropped %d, want %d", c.Dropped(), want)
	}
}
