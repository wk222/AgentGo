// Package buildinfo identifies one build of AgentGo for support: the release version stamped in when the
// binary was built, the toolchain, the operating system it runs on and the build tags (web or desktop).
// Stdlib (and x/sys on Windows) only.
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// Version is the release name. The release scripts stamp it:
//
//	go build -ldflags "-X agentgo/internal/buildinfo.Version=0.1.0-win7.8"
//
// A binary built by hand reports "dev".
var Version = "dev"

// Info is what a support request needs to know about the binary and the machine it runs on.
type Info struct {
	Version   string `json:"version"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	OSVersion string `json:"os_version,omitempty"`
	Arch      string `json:"arch"`
	Tags      string `json:"tags,omitempty"`
}

// Current describes this build and the system it is running on.
func Current() Info {
	info := Info{
		Version:   Version,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		OSVersion: osVersion(),
		Arch:      runtime.GOARCH,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "-tags" {
				info.Tags = s.Value
			}
		}
	}
	return info
}
