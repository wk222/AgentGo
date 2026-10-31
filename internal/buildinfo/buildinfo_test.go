package buildinfo

import (
	"runtime"
	"strings"
	"testing"
)

func TestCurrentDescribesTheBuildAndTheMachine(t *testing.T) {
	info := Current()
	if info.Version != Version || info.GoVersion != runtime.Version() || info.OS != runtime.GOOS || info.Arch != runtime.GOARCH {
		t.Fatalf("%+v", info)
	}
	if runtime.GOOS == "windows" {
		if parts := strings.Split(info.OSVersion, "."); len(parts) != 3 {
			t.Fatalf("Windows version %q is not major.minor.build", info.OSVersion)
		}
	}
}
