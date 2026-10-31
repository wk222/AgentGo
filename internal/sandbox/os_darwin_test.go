//go:build darwin

package sandbox

import (
	"strings"
	"testing"
)

func TestBuildProfileDefaults(t *testing.T) {
	p := Policy{WriteRoots: []string{"/tmp"}, ReadRoots: []string{"/tmp"}}
	prof := buildProfile(p)
	for _, want := range []string{"(deny file-write*)", "(allow file-write*", "(deny network*)"} {
		if !strings.Contains(prof, want) {
			t.Fatalf("profile missing %q:\n%s", want, prof)
		}
	}
	p.AllowNetwork = true
	if strings.Contains(buildProfile(p), "(deny network*)") {
		t.Fatal("AllowNetwork must drop the network deny")
	}
}
