package applog

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCrashHelperProcess is not a test: the tests below re-run the test binary to make it crash for real.
func TestCrashHelperProcess(t *testing.T) {
	dir := os.Getenv("AGENTGO_CRASH_DIR")
	if dir == "" {
		t.Skip("helper process for the crash tests")
	}
	if err := InitCrash(dir); err != nil {
		os.Stderr.WriteString("InitCrash: " + err.Error() + "\n")
		os.Exit(3)
	}
	go func() { panic("boom from an unguarded goroutine") }()
	select {}
}

func TestACrashLandsInTheCrashLogAndIsReportedOnTheNextStart(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelperProcess$")
	cmd.Env = append(os.Environ(), "AGENTGO_CRASH_DIR="+dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the helper process should have crashed:\n%s", out)
	}

	data, rerr := os.ReadFile(filepath.Join(dir, crashFileName))
	if rerr != nil {
		t.Fatalf("no crash log: %v\n%s", rerr, out)
	}
	if text := string(data); !strings.Contains(text, "boom from an unguarded goroutine") || !strings.Contains(text, "goroutine") {
		t.Fatalf("the crash log does not hold the panic and its stack:\n%s", text)
	}

	// The next start reports it, keeps the evidence and begins with an empty crash.log.
	if err := InitCrash(dir); err != nil {
		t.Fatal(err)
	}
	defer CloseCrash()
	info, ok := PreviousCrash()
	if !ok {
		t.Fatal("the crash of the previous run was not reported")
	}
	if !strings.Contains(info.Headline, "boom from an unguarded goroutine") || !strings.HasPrefix(info.Headline, "panic:") {
		t.Fatalf("headline = %q", info.Headline)
	}
	if info.Size == 0 || info.Time.IsZero() || filepath.Base(info.File) != crashLastFileName {
		t.Fatalf("incomplete crash info: %+v", info)
	}
	if kept, err := os.ReadFile(info.File); err != nil || !strings.Contains(string(kept), "boom") {
		t.Fatalf("the evidence was not kept: %v", err)
	}
	if st, err := os.Stat(filepath.Join(dir, crashFileName)); err != nil || st.Size() != 0 {
		t.Fatalf("a fresh, empty crash.log should be waiting for the next crash: %v %v", st, err)
	}
}

func TestACleanRunReportsNoCrash(t *testing.T) {
	dir := t.TempDir()
	if err := InitCrash(dir); err != nil { // first start ever
		t.Fatal(err)
	}
	if _, ok := PreviousCrash(); ok {
		t.Fatal("nothing has crashed yet")
	}
	// The run ended normally: crash.log is still empty, so the next start has nothing to report either.
	if err := InitCrash(dir); err != nil {
		t.Fatal(err)
	}
	defer CloseCrash()
	if _, ok := PreviousCrash(); ok {
		t.Fatal("an empty crash.log is not a crash")
	}
	if _, err := os.Stat(filepath.Join(dir, crashLastFileName)); !os.IsNotExist(err) {
		t.Fatalf("no crash evidence should exist: %v", err)
	}
}

func TestCrashHeadlinePicksTheLineThatNamesTheFailure(t *testing.T) {
	cases := map[string]string{
		"panic: runtime error: index out of range [3] with length 2\n\ngoroutine 1 [running]:\n": "panic: runtime error: index out of range [3] with length 2",
		"\n\nfatal error: concurrent map writes\n":                                               "fatal error: concurrent map writes",
		"some preface\npanic: late\n":                                                            "panic: late",
		"only an odd line\nanother\n":                                                            "only an odd line",
		"":                                                                                       "",
	}
	for in, want := range cases {
		if got := crashHeadline(in); got != want {
			t.Errorf("crashHeadline(%q) = %q, want %q", in, got, want)
		}
	}
	long := "panic: " + strings.Repeat("字", 400)
	if got := crashHeadline(long); len([]rune(got)) != crashHeadlineMax+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("a long headline is not clipped to %d runes: %d", crashHeadlineMax, len([]rune(got)))
	}
}
