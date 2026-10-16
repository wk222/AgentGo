package agent

import "testing"

func TestHarnessPresetsRoundTripThroughSessionMode(t *testing.T) {
	hs := Harnesses()
	if len(hs) != 4 {
		t.Fatalf("expected 4 presets, got %d", len(hs))
	}
	seen := map[string]bool{}
	for _, h := range hs {
		if h.ID == "" || h.Name == "" || h.Label == "" || h.Desc == "" || seen[h.ID] {
			t.Errorf("bad or duplicate preset %+v", h)
		}
		seen[h.ID] = true
		if got := h.Mode().Harness(); got.ID != h.ID {
			t.Errorf("%s: Mode().Harness() = %s (mode %+v)", h.ID, got.ID, h.Mode())
		}
		if got := h.Mode().Normalized(); got != h.Mode() {
			t.Errorf("%s: preset mode is not normalized: %+v -> %+v", h.ID, h.Mode(), got)
		}
	}
	if _, ok := HarnessByID(DefaultHarnessID); !ok {
		t.Fatal("default harness id must exist")
	}
	if got := DefaultSessionMode().Harness().ID; got != DefaultHarnessID {
		t.Errorf("DefaultSessionMode is %s, want %s", got, DefaultHarnessID)
	}
}

func TestHarnessOfNonPresetCombinations(t *testing.T) {
	cases := []struct {
		m    SessionMode
		want string
	}{
		{SessionMode{Profile: ModeAppMatrix, Canvas: CanvasDeep}, HarnessCreative},
		{SessionMode{Profile: ModeAppMatrix, Canvas: CanvasFocused}, HarnessCreative},
		{SessionMode{Profile: ModeAdmin, Canvas: CanvasFocused}, HarnessUltimate},
		{SessionMode{Profile: ModeAssistant, Canvas: CanvasDeep}, HarnessUltimate},
		{SessionMode{}, HarnessStandard},
		{SessionMode{Profile: "garbage", Canvas: "garbage"}, HarnessStandard},
	}
	for _, c := range cases {
		if got := c.m.Harness().ID; got != c.want {
			t.Errorf("%+v -> %s, want %s", c.m, got, c.want)
		}
	}
}

func TestResolveHarnessIsCaseInsensitiveAndKnowsLegacyIDs(t *testing.T) {
	for in, want := range map[string]string{
		"Ultimate": HarnessUltimate, " minimal ": HarnessMinimal,
		"architect": HarnessUltimate, "coder": HarnessStandard, "ask": HarnessMinimal,
	} {
		h, ok := ResolveHarness(in)
		if !ok || h.ID != want {
			t.Errorf("ResolveHarness(%q) = %v, %v; want %s", in, h.ID, ok, want)
		}
	}
	if _, ok := ResolveHarness("nope"); ok {
		t.Error("unknown id must not resolve")
	}
}

func TestHarnessesReturnsACopy(t *testing.T) {
	a := Harnesses()
	a[0].Name = "changed"
	if Harnesses()[0].Name == "changed" {
		t.Fatal("Harnesses must not expose the internal slice")
	}
}
