package engine

import (
	"context"
	"reflect"
	"testing"
)

// uncomparable has a func field, so comparing two interface values holding it panics.
type uncomparable struct {
	fakeEngine
	hook func()
}

func TestRegisterDisposeRemovesAndRepicksDefault(t *testing.T) {
	r := NewRouter()
	noop := func(context.Context, RunRequest, Emitter) (RunResult, error) { return RunResult{}, nil }
	disposeA := r.Register(&fakeEngine{id: "a", run: noop})
	disposeB := r.Register(&fakeEngine{id: "b", run: noop})
	r.Register(&fakeEngine{id: "c", run: noop})
	if got := r.Engines(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("engines = %v", got)
	}
	disposeB() // not the default
	if got := r.Engines(); !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Fatalf("after b: %v", got)
	}
	disposeA() // the default goes away -> alphabetically first remaining takes over
	if got := r.Engines(); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("after a: %v", got)
	}
	if _, err := r.SetDefault("c"), error(nil); err != nil {
		t.Fatal(err)
	}
	disposeA() // idempotent
	disposeB()
	if got := r.Engines(); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("repeat dispose changed state: %v", got)
	}
}

func TestStaleDisposeDoesNotRemoveReplacement(t *testing.T) {
	r := NewRouter()
	noop := func(context.Context, RunRequest, Emitter) (RunResult, error) { return RunResult{}, nil }
	old := r.Register(&uncomparable{fakeEngine: fakeEngine{id: "x", run: noop}, hook: func() {}})
	r.Register(&uncomparable{fakeEngine: fakeEngine{id: "x", run: noop}, hook: func() {}}) // replaces
	old()                                                                                // must not panic or remove the new one
	if got := r.Engines(); !reflect.DeepEqual(got, []string{"x"}) {
		t.Fatalf("engines = %v", got)
	}
}

func TestEmptyRouterAfterLastDispose(t *testing.T) {
	r := NewRouter()
	noop := func(context.Context, RunRequest, Emitter) (RunResult, error) { return RunResult{}, nil }
	r.Register(&fakeEngine{id: "only", run: noop})()
	res := r.Run(context.Background(), RunRequest{Input: "x"}, nil)
	if res.Error == "" {
		t.Fatal("running on an empty router must report an error")
	}
	r.Register(&fakeEngine{id: "back", run: noop})
	if res := r.Run(context.Background(), RunRequest{Input: "x"}, nil); res.Error != "" {
		t.Fatalf("default should be re-established: %q", res.Error)
	}
}
