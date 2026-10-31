//go:build windows

package tools

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestMCPCloseTerminatesStubbornPeerAndItsChild(t *testing.T) {
	r := contractRegistry(t)
	childReady := filepath.Join(t.TempDir(), "child-pid")
	loadPeer(t, r, "stubborn", mcpPeerConfig{Label: "stubborn", Tools: []string{"ping"}, ReadOnly: true, Stubborn: true, ChildReady: childReady})
	waitPeerFile(t, childReady)
	raw, err := os.ReadFile(childReady)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatal("child not alive before close", err)
	}
	defer windows.CloseHandle(handle)
	done := make(chan struct{})
	go func() { r.CloseMCPServers(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("close waited forever on noncooperative peer")
	}
	status, err := windows.WaitForSingleObject(handle, 3000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatal("MCP child outlived its registry", status, err)
	}
	if len(r.ListMCPServers()) != 0 {
		t.Fatal("closed registry retained services")
	}
}
