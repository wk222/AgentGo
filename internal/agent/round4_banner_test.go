package agent

// Offline proofs for the banner-ablation diagnostic.

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// The ablation is only meaningful if "banner stripped" is exactly the plain
// assistant prompt, not some third variant. Compare against the production
// builder itself so a change to the banner text cannot silently break this.
func TestRound4StripBannerEqualsAssistantPrompt(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	red := SessionMode{Profile: ModeRedTeam, Canvas: CanvasBalanced}.ModeHints()
	asst := SessionMode{Profile: ModeAssistant, Canvas: CanvasBalanced}.ModeHints()
	if !strings.Contains(red, "[Armor-break session]") || !strings.Contains(red, "Armor session memories") {
		t.Fatalf("red_team prompt must carry the banner and memories under the flag:\n%s", red)
	}
	if strings.Contains(asst, "armor-break") || strings.Contains(asst, "Armor session") {
		t.Fatalf("assistant prompt must not carry any armor text:\n%s", asst)
	}
	if got := round4StripArmorBanner(red); got != asst {
		t.Fatalf("stripped red_team prompt differs from the assistant prompt\n--- stripped ---\n%s\n--- assistant ---\n%s", got, asst)
	}

	// Wrapped the way AgentGo does (rules block + isolation block).
	wrap := func(h, profile string) string {
		return "<system_rules>\nbase rules\n\n" + h + "\n</system_rules>\n\n<isolation>\n[Isolation] profile=" + profile + " workspace_root=X\n</isolation>"
	}
	if got, want := round4StripArmorBanner(wrap(red, "red_team")), wrap(asst, "assistant"); got != want {
		t.Fatalf("stripping the wrapped prompt must equal the wrapped assistant prompt\n%s\n---\n%s", got, want)
	}
	if got := round4StripArmorBanner(wrap(asst, "assistant")); got != wrap(asst, "assistant") {
		t.Fatal("stripping an already-plain prompt must be a no-op")
	}
}

func TestRound4ProxyAppliesSystemTransformOnlyToSystemMessages(t *testing.T) {
	up := newFakeUpstream(t, func(body string) map[string]any {
		return map[string]any{"role": "assistant", "content": "ok"}
	})
	p := newLLMProxy(up.srv.URL, "任务", "")
	defer p.Close()
	p.SetSystemTransform(func(s string) string { return strings.ReplaceAll(s, "SECRET-BANNER", "") })

	body := `{"model":"m","tools":[{"type":"function","function":{"name":"x"}}],"temperature":0.2,"messages":[` +
		`{"role":"system","content":"rules SECRET-BANNER end"},` +
		`{"role":"user","content":"任务 SECRET-BANNER 保持不变"}]}`
	resp, err := http.Post(p.URL()+"/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	sent := up.Bodies()
	if len(sent) != 1 {
		t.Fatalf("want one upstream request, got %d", len(sent))
	}
	if strings.Contains(sent[0], `rules SECRET-BANNER`) {
		t.Fatalf("system message was not transformed: %s", sent[0])
	}
	if !strings.Contains(sent[0], `rules  end`) {
		t.Fatalf("transformed system content missing: %s", sent[0])
	}
	if !strings.Contains(sent[0], `任务 SECRET-BANNER 保持不变`) {
		t.Fatalf("user message must be untouched: %s", sent[0])
	}
	for _, keep := range []string{`"temperature":0.2`, `"tools"`, `"model":"m"`} {
		if !strings.Contains(sent[0], keep) {
			t.Fatalf("other request fields must survive the rewrite (%s): %s", keep, sent[0])
		}
	}
	if turns := p.Turns(); len(turns) != 1 || !turns[0].TaskInContext {
		t.Fatalf("recording must still see the task: %+v", turns)
	}
}
