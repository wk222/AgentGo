package workflow

import (
	"context"
	"testing"
	"time"
)

func TestSignalBusEmitWait(t *testing.T) {
	bus := NewSignalBus()
	runID := "run-1"
	go func() {
		time.Sleep(20 * time.Millisecond)
		bus.Emit(runID, "go", "payload")
	}()
	ctx := context.Background()
	out, err := bus.Wait(ctx, runID, "go", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if out != "payload" {
		t.Fatalf("got %q", out)
	}
}
