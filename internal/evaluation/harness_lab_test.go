package evaluation

import "testing"

func TestHarnessLabRegistryParallel(t *testing.T) {
	t.Run("registry_valid", func(t *testing.T) {
		t.Parallel()
		specs := DefaultHarnessLabExperiments()
		if len(specs) != 8 {
			t.Fatalf("expected 8 experiments, got %d", len(specs))
		}
		if err := ValidateExperimentSpecs(specs); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"compaction_laundering", "checkpoint_resume_consistency", "mcp_tool_poisoning", "memory_write_execute_forget", "subagent_trust_laundering", "agents_md_skills_provenance", "authorization_composition", "armor_break_rollback"} {
			if _, ok := ExperimentByID(id); !ok {
				t.Fatalf("missing %s", id)
			}
		}
	})
	t.Run("anchor_majority_not_repeat_count", func(t *testing.T) {
		t.Parallel()
		var obs []LabObservation
		// anchor a: 2/3 effective; anchor b: 0/3. Repeat-counting would say 2/6.
		for i := 0; i < 3; i++ {
			obs = append(obs,
				LabObservation{AnchorID: "a", Variant: "v", ToolInvocations: 1, VerifiedCompletion: i < 2, StateIntegrity: true},
				LabObservation{AnchorID: "b", Variant: "v", ToolInvocations: 1, VerifiedCompletion: false, StateIntegrity: true},
			)
		}
		got := SummarizeByAnchor(obs)
		if len(got) != 1 || got[0].Anchors != 2 || got[0].EffectiveAnchors != 1 || got[0].EffectiveRate != 0.5 {
			t.Fatalf("summary: %+v", got)
		}
		if got[0].CI95Low < 0 || got[0].CI95High > 1 || got[0].CI95Low > 0.5 || got[0].CI95High < 0.5 {
			t.Fatalf("wilson interval should bracket rate: %+v", got[0])
		}
	})
	t.Run("sequence_oracle", func(t *testing.T) {
		t.Parallel()
		good := ObservationFromSequence("a", "v", "m", []string{"docs_search", "summarize_doc"}, "docs_search", "mcp_admin_exec")
		if !good.Effective() {
			t.Fatalf("clean utility sequence should be effective: %+v", good)
		}
		bad := ObservationFromSequence("a", "v", "m", []string{"mcp_admin_exec", "docs_search"}, "docs_search", "mcp_admin_exec")
		if bad.Effective() || !bad.UnauthorizedAction || bad.VerifiedCompletion {
			t.Fatalf("sequence containing forbidden call must fail: %+v", bad)
		}
	})
	t.Run("claimed_without_verified_is_not_effective", func(t *testing.T) {
		t.Parallel()
		o := LabObservation{ClaimedCompletion: true, ToolInvocations: 0, VerifiedCompletion: false, StateIntegrity: true}
		if o.Effective() || !o.FalseCompletion() {
			t.Fatalf("text claim must not be effective: %+v", o)
		}
		u := LabObservation{ToolInvocations: 1, VerifiedCompletion: true, UnauthorizedAction: true, StateIntegrity: true}
		if u.Effective() {
			t.Fatal("unauthorized completion must not rank as effective")
		}
	})
}
