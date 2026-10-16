package bridge

import (
	"context"
	"encoding/json"
	"testing"

	"agentgo/internal/agent"
)

// Desktop IPC, the ACP editor bridge and the gateway must all expose the same presets.
func TestEveryClientSurfaceListsTheSameHarnesses(t *testing.T) {
	want := agent.Harnesses()

	ipc := (&AppService{}).ListHarnesses()
	got, ok := ipc["harnesses"].([]agent.Harness)
	if !ok || len(got) != len(want) {
		t.Fatalf("IPC harnesses = %#v", ipc["harnesses"])
	}

	items := acpModeItems()
	if len(items) != len(want) {
		t.Fatalf("ACP modes = %d, want %d", len(items), len(want))
	}
	for i, h := range want {
		if got[i].ID != h.ID || items[i].ID != h.ID || items[i].Name != h.Label || items[i].Description != h.Desc {
			t.Errorf("preset %d differs between surfaces: ipc=%v acp=%+v want=%+v", i, got[i], items[i], h)
		}
	}

	raw, err := (&RuntimeGateway{}).ListHarnesses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var gw struct {
		Harnesses []agent.Harness `json:"harnesses"`
		Default   string          `json:"default"`
	}
	if err := json.Unmarshal(raw, &gw); err != nil {
		t.Fatal(err)
	}
	if len(gw.Harnesses) != len(want) || gw.Default != agent.DefaultHarnessID {
		t.Fatalf("gateway harnesses = %+v", gw)
	}
	if ipc["default"] != agent.DefaultHarnessID {
		t.Errorf("IPC default = %v", ipc["default"])
	}
}

func TestSetSessionHarnessRejectsUnknownID(t *testing.T) {
	res := (&AppService{}).SetSessionHarness("s1", "does-not-exist")
	if res["success"] != false {
		t.Fatalf("unknown harness must fail: %v", res)
	}
}
