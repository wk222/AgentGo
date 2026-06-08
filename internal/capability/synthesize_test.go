package capability

import (
	"context"
	"testing"
)

func TestSynthesizePipeline(t *testing.T) {
	bus := NewBus()
	p := NewSynthesizePipeline(bus)
	g, err := p.CompileAndRegister(context.Background(), SynthesizeRequest{
		Kind: "tool", Name: "demo_tool", Scope: "agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != "demo_tool" {
		t.Fatalf("grant name %q", g.Name)
	}
	list := bus.List("tool")
	if len(list) == 0 {
		t.Fatal("expected grant in bus")
	}
}
