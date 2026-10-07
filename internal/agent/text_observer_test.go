package agent

import (
	"context"
	"testing"
)

func TestCheckpointTextEmitterStaysWithRun(t *testing.T) {
	var got string
	ctx := WithTextEmitter(context.Background(), func(delta string) { got += delta })
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	textEmitterFrom(child)("resume")
	if got != "resume" || textEmitterFrom(context.Background()) != nil {
		t.Fatal("emitter leaked or lost across run context")
	}
}
