//go:build windows

package userhooks

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

var procGetACP = syscall.NewLazyDLL("kernel32.dll").NewProc("GetACP")

// A Chinese Windows prints GBK from cmd's echo, Python pipes and PowerShell 5; the
// reason a hook gives must still read as Chinese. Other code pages decode other
// bytes, so the check only means something where the code page is 936.
func TestDecodeOutputReadsGBKOnAChineseWindows(t *testing.T) {
	if acp, _, _ := procGetACP.Call(); acp != 936 {
		t.Skipf("the ANSI code page is %d, not 936 (GBK)", acp)
	}
	gbk := "\xb9\xab\xcb\xbe\xb9\xe6\xb6\xa8" // 公司规定
	if got := decodeOutput(gbk); got != "公司规定" {
		t.Errorf("GBK bytes decode to %q", got)
	}
	if got := decodeOutput("GUARD-7731 " + gbk + "\r\n"); got != "GUARD-7731 公司规定\r\n" {
		t.Errorf("mixed ASCII and GBK decode to %q", got)
	}
}

// What the console of a child process prints depends on how the app was
// started, so the same hook may print UTF-8 one day and GBK the next.
func TestAHookThatPrintsGBKStillGivesAReadableReason(t *testing.T) {
	if acp, _, _ := procGetACP.Call(); acp != 936 {
		t.Skipf("the ANSI code page is %d, not 936 (GBK)", acp)
	}
	run := runScript(t.Context(), Hook{Command: helperLine("gbk-reason"), Timeout: 30 * time.Second}, "", nil, nil)
	if run.Code != 2 || !strings.Contains(run.Stderr, "公司规定") {
		t.Errorf("the reason was lost: %+v", run)
	}
}
