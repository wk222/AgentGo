package agent

import (
	"strings"
	"testing"
)

func TestArmorMemoriesAreEphemeralAndOperatorSourced(t *testing.T) {
	ms := DefaultArmorMemories()
	if len(ms) != 4 {
		t.Fatalf("expected 4 armor memories, got %d", len(ms))
	}
	seen := map[string]bool{}
	for _, m := range ms {
		if m.Persistent || m.CrossSession || m.ExecutionEvidence {
			t.Fatalf("armor memory must be ephemeral and non-evidence: %+v", m)
		}
		if m.Scope != "session_only" || !strings.HasPrefix(m.Provenance, "operator_env:") {
			t.Fatalf("bad provenance/scope: %+v", m)
		}
		if seen[m.ID] || m.ID == "" || m.Content == "" {
			t.Fatalf("bad memory: %+v", m)
		}
		seen[m.ID] = true
	}
}

func TestArmorMemoryPromptGating(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	red := SessionMode{Profile: ModeRedTeam, Canvas: CanvasDeep}
	p := ArmorMemoryPrompt(red)
	for _, id := range ArmorMemoryIDs() {
		if !strings.Contains(p, id) {
			t.Fatalf("prompt missing %s: %s", id, p)
		}
	}
	if !strings.Contains(p, "not long-term memory") || !strings.Contains(p, "not execution evidence") {
		t.Fatalf("prompt must state limits: %s", p)
	}
	if got := ArmorMemoryPrompt(SessionMode{Profile: ModeAssistant, Canvas: CanvasBalanced}); got != "" {
		t.Fatalf("assistant profile must not receive armor memories: %s", got)
	}
	if !strings.Contains(red.ModeHints(), "armor.operator_authority") {
		t.Fatal("red_team ModeHints should include armor memories when enabled")
	}
	t.Setenv("AGENTGO_ARMOR_BREAK", "0")
	if got := ArmorMemoryPrompt(red); got != "" {
		t.Fatalf("flag off must disable armor memories: %s", got)
	}
}

func TestArmorAuditMetaListsMemoryIDs(t *testing.T) {
	meta := armorAuditMeta("抱歉，我无法协助。")
	for _, id := range ArmorMemoryIDs() {
		if !strings.Contains(meta, id) {
			t.Fatalf("audit meta missing memory id %s: %s", id, meta)
		}
	}
}
