package agent

import "testing"

func TestCanvasEinoTriggers(t *testing.T) {
	if CanvasPolicies[CanvasFocused].SummarizationTokens >= CanvasPolicies[CanvasDeep].SummarizationTokens {
		t.Fatal("focused should summarize earlier than deep")
	}
	m := SessionMode{Canvas: CanvasFocused}
	if m.SummarizationTokenTrigger() != 60_000 {
		t.Fatalf("got %d", m.SummarizationTokenTrigger())
	}
	if m.MaxIterations() != 6 {
		t.Fatalf("iterations=%d", m.MaxIterations())
	}
}
