package config

import (
	"strings"
	"testing"
)

func TestBoolParsingAndDefaults(t *testing.T) {
	on := newBoolForTest("AGENTGO_T_ON", true)
	off := newBoolForTest("AGENTGO_T_OFF", false)

	for _, v := range []string{"1", "true", "TRUE", "on", "yes"} {
		t.Setenv("AGENTGO_T_OFF", v)
		if !off.On() {
			t.Errorf("%q must turn a default-off flag on", v)
		}
	}
	for _, v := range []string{"0", "false", "Off", "no"} {
		t.Setenv("AGENTGO_T_ON", v)
		if on.On() {
			t.Errorf("%q must turn a default-on flag off", v)
		}
	}
	t.Setenv("AGENTGO_T_ON", "garbage")
	if !on.On() {
		t.Error("unparsable value must fall back to the default (on)")
	}
	t.Setenv("AGENTGO_T_ON", "  ")
	if !on.On() {
		t.Error("blank value counts as unset")
	}
}

func TestLookupDistinguishesUnsetFromExplicit(t *testing.T) {
	f := newBoolForTest("AGENTGO_T_TRI", false)
	if _, ok := f.Lookup(); ok {
		t.Fatal("unset flag must report ok=false")
	}
	t.Setenv("AGENTGO_T_TRI", "0")
	if on, ok := f.Lookup(); !ok || on {
		t.Fatalf("explicit 0 = (%v,%v), want (false,true)", on, ok)
	}
}

func TestTypedHandlesUseDeclaredDefaults(t *testing.T) {
	if got := EmbeddingDim.Get(); got != 1536 {
		t.Errorf("EmbeddingDim default = %d", got)
	}
	t.Setenv("AGENTGO_EMBEDDING_DIM", "not-a-number")
	if got := EmbeddingDim.Get(); got != 1536 {
		t.Errorf("invalid int must fall back to default, got %d", got)
	}
	t.Setenv("AGENTGO_RECALL_FTS_WEIGHT", "0.9")
	if got := RecallFTSWeight.Get(); got != 0.9 {
		t.Errorf("float override = %v", got)
	}
	if got := Model.Get(); got != "gpt-4o-mini" {
		t.Errorf("Model default = %q", got)
	}
}

func TestSetDefaultDoesNotOverrideUser(t *testing.T) {
	t.Setenv("AGENTGO_DISABLE_SCHEDULER", "")
	DisableScheduler.SetDefault(true)
	if !DisableScheduler.On() {
		t.Fatal("SetDefault must apply when unset")
	}
	t.Setenv("AGENTGO_DISABLE_SCHEDULER", "0")
	DisableScheduler.SetDefault(true)
	if DisableScheduler.On() {
		t.Fatal("SetDefault must not override an explicit user value")
	}
}

func TestSnapshotMasksSecretsAndIsSorted(t *testing.T) {
	t.Setenv("AGENTGO_API_KEY", "sk-super-secret")
	snap := Snapshot()
	if len(snap) < 50 {
		t.Fatalf("snapshot has %d flags", len(snap))
	}
	for i, e := range snap {
		if strings.Contains(e.Value, "sk-super-secret") {
			t.Fatalf("secret leaked in snapshot: %+v", e)
		}
		if e.Name == "AGENTGO_API_KEY" && (!e.Set || e.Value != "********") {
			t.Fatalf("secret entry = %+v", e)
		}
		if !strings.HasPrefix(e.Name, "AGENTGO_") || e.Doc == "" {
			t.Fatalf("flag %q needs the AGENTGO_ prefix and a doc string", e.Name)
		}
		if i > 0 && snap[i-1].Group == e.Group && snap[i-1].Name > e.Name {
			t.Fatalf("snapshot not sorted at %s", e.Name)
		}
	}
}

func TestDuplicateDeclarationPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("declaring the same flag twice must panic")
		}
	}()
	newBool("AGENTGO_MODEL", false, GroupModel, "dup")
}

func newBoolForTest(name string, def bool) Bool {
	i := Info{Name: name, Kind: KindBool, Default: "0", Group: GroupDebug, Doc: "test"}
	if def {
		i.Default = "1"
	}
	return Bool{i}
}
