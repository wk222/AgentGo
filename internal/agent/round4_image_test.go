package agent

// Characterisation of the in-memory `images` argument of Runner.Generate.
//
// runADK appends those images through adk.WithHistoryModifier. eino documents
// that option as a resume-time hook ("modify history during resume") and
// implements it with compose.WithStateModifier, which the graph only invokes
// while restoring a checkpoint. The armor-break rollback uses the same option on
// a fresh run, and TestRound4ProductionArmorModelViewKeepsHesitantTurn shows
// that rollback never reaches the model. This test asks the same question for
// the image parameter: does an image passed to a fresh Generate call appear in
// the request body?
//
// Scope: it exercises only the in-memory argument. A second attachment route
// exists (session_spine_mw.go re-attaches images named in a persisted user
// message's meta from <dataDir>/attachments); the desktop path through it is NOT
// exercised here, so this test does not show that attachments are broken in the
// product. It needs no credential or network: a recording fake upstream
// captures the exact request body AgentGo would send.

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRound4InMemoryImageArgumentOnFreshRun(t *testing.T) {
	sb := newSandbox(t.TempDir())
	runner, _, closeRunner, err := newRound4Runner(sb, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer closeRunner()

	up := newFakeUpstream(t, func(body string) map[string]any {
		return map[string]any{"role": "assistant", "content": "收到"}
	})
	cfg := LLMSettings{APIBase: up.srv.URL, APIKey: "test-only-not-a-key", Model: "fake"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = WithSessionMode(ctx, SessionMode{Profile: ModeAssistant, Canvas: CanvasBalanced})

	const img = "SU1BR0VQQVlMT0FEMDEyMzQ1Njc4OQ==" // distinctive base64-looking payload
	if _, err := runner.Generate(ctx, cfg, "img-fresh", "这张图是什么？", []string{img}); err != nil {
		t.Fatal(err)
	}
	bodies := up.Bodies()
	if len(bodies) == 0 {
		t.Fatal("no request reached the fake upstream")
	}
	first := bodies[0]
	hasPayload := strings.Contains(first, img)
	hasImagePart := strings.Contains(first, "image_url")
	t.Logf("first request: bytes=%d image payload present=%v image_url part present=%v", len(first), hasPayload, hasImagePart)
	if !hasPayload && !hasImagePart {
		t.Logf("OBSERVATION: the in-memory images argument does not reach the model request on a fresh run (modifier path only); the persisted-attachment route was not exercised")
	}
}
