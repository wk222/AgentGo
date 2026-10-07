package bridge

import (
	"strings"
	"testing"

	"agentgo/internal/plugin"
)

func capByID(cs []Capability, id string) Capability {
	for _, c := range cs {
		if c.ID == id {
			return c
		}
	}
	return Capability{}
}

func TestProbeSaysWhyACapabilityIsUnavailable(t *testing.T) {
	cs := probeCapabilities([]plugin.Status{
		{Name: "eino", State: plugin.StateRunning},
		{Name: "memory", State: plugin.StateRunning},
		{Name: "distill", State: plugin.StatePending, Disabled: true},
		{Name: "ide", State: plugin.StateFailed, Error: "boom"},
		{Name: "scheduler", State: plugin.StateSkipped, Error: `needs service "db"`},
		{Name: "workflow", State: plugin.StateStopped},
		{Name: "approvals", State: plugin.StatePending},
	})

	if c := capByID(cs, "chat.eino"); !c.Available || c.Reason != "" {
		t.Errorf("running plugin: %+v", c)
	}
	for id, want := range map[string]string{
		"memory.distill": "disabled by configuration",
		"ide":            "failed to start: boom",
		"scheduler":      `skipped`,
		"workflow":       "is stopped",
		"approvals":      "has not started",
		"chat.crush":     "AGENTGO_CRUSH_EXE", // not registered at all: how to enable it
		"gateway":        "AGENTGO_GATEWAY_ADDR",
	} {
		c := capByID(cs, id)
		if c.Available || !strings.Contains(c.Reason, want) {
			t.Errorf("%s: want unavailable with %q, got %+v", id, want, c)
		}
	}
	// A capability that needs two plugins is unavailable when either is.
	if c := capByID(cs, "memory.distill"); c.Available || len(c.Plugins) != 2 {
		t.Errorf("memory.distill: %+v", c)
	}
}

func TestProbeWithNoHostReportsEverythingUnavailable(t *testing.T) {
	cs := probeCapabilities(nil)
	if len(cs) != len(capabilityDefs) {
		t.Fatalf("got %d capabilities", len(cs))
	}
	for _, c := range cs {
		if c.Available || c.Reason == "" {
			t.Errorf("%+v", c)
		}
	}
}

// Every plugin a capability names must exist, or the probe would report
// "not enabled" for a typo forever.
func TestCapabilitiesNameRealPlugins(t *testing.T) {
	known := map[string]bool{"eino": true, "crush": true, "crushproto": true, "codetools": true, "host": true}
	for _, p := range (&Runtime{}).runtimePlugins() {
		known[p.Name] = true
	}
	for _, d := range capabilityDefs {
		for _, n := range d.needs {
			if !known[n] {
				t.Errorf("capability %s needs unknown plugin %q", d.id, n)
			}
		}
	}
}

// On a real runtime the probe follows the host: disabling or failing a plugin
// changes the answer, and restoring it restores the answer.
func TestCapabilitiesFollowTheRunningHost(t *testing.T) {
	t.Setenv("AGENTGO_CRUSH_EXE", "")
	rt := boot(t)
	s := NewAppService(rt)
	t.Cleanup(s.Close)

	cs := s.Capabilities()
	for _, id := range []string{"chat.eino", "tools.builtin", "workflow", "approvals", "memory", "ide", "scheduler", "code.tools"} {
		if c := capByID(cs, id); !c.Available {
			t.Errorf("%s should be available on a fresh runtime: %+v", id, c)
		}
	}
	if c := capByID(cs, "protocol.crush"); c.Available || !strings.Contains(c.Reason, "AGENTGO_CRUSHPROTO_ADDR") {
		t.Errorf("protocol.crush: %+v", c)
	}

	if out := s.StopPlugin("ide"); out["success"] != true {
		t.Fatalf("stop ide: %+v", out)
	}
	if c := capByID(s.Capabilities(), "ide"); c.Available || !strings.Contains(c.Reason, "stopped") {
		t.Errorf("ide after stop: %+v", c)
	}
	if out := s.ReloadPlugin("ide"); out["success"] != true {
		t.Fatalf("reload ide: %+v", out)
	}
	if c := capByID(s.Capabilities(), "ide"); !c.Available {
		t.Errorf("ide after reload: %+v", c)
	}
}
