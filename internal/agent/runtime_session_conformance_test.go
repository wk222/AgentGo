/*
 * Copyright 2026 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

type conformanceExtensionPayload struct {
	OK bool `json:"ok"`
}

func init() {
	schema.RegisterName[*conformanceExtensionPayload]("_eino_adk_session_conformance_extension_payload")
}

func runtimeContract_runConformanceTests[M adk.MessageType](t *testing.T, factory func(testing.TB) adk.SessionEventStore[M], makeMessage func(content string) M) {
	t.Helper()

	t.Run("AppendEvents and forward LoadEvents", func(t *testing.T) { runtimeContract_testAppendAndForwardLoad(t, factory, makeMessage) })
	t.Run("LoadEvents reverse pagination", func(t *testing.T) { runtimeContract_testReversePagination(t, factory, makeMessage) })
	t.Run("After forward pagination", func(t *testing.T) { runtimeContract_testForwardPagination(t, factory, makeMessage) })
	t.Run("sessionID isolates events", func(t *testing.T) { runtimeContract_testSessionIsolation(t, factory, makeMessage) })
	t.Run("Empty session returns no events", func(t *testing.T) { runtimeContract_testEmptySession(t, factory) })
	t.Run("AppendEvents rejects non-replay duplicate EventID", func(t *testing.T) { runtimeContract_testRejectDuplicateEventID(t, factory, makeMessage) })
	t.Run("AppendEvents rejects duplicate EventID within same batch", func(t *testing.T) { runtimeContract_testRejectDuplicateEventIDWithinBatch(t, factory, makeMessage) })
	t.Run("AppendEvents rejects empty EventID with ErrInvalidEventID", func(t *testing.T) { runtimeContract_testRejectEmptyEventID(t, factory, makeMessage) })
	t.Run("After resumes by EventID forward", func(t *testing.T) { runtimeContract_testAfterForward(t, factory, makeMessage) })
	t.Run("After resumes by EventID reverse", func(t *testing.T) { runtimeContract_testAfterReverse(t, factory, makeMessage) })
	t.Run("Unknown After returns ErrEventIDOutOfRange", func(t *testing.T) { runtimeContract_testUnknownAfter(t, factory, makeMessage) })
	t.Run("Empty page when After=last forward and After=first reverse", func(t *testing.T) { runtimeContract_testEmptyPageBoundary(t, factory, makeMessage) })
	t.Run("Extension kind filters correctly", func(t *testing.T) { runtimeContract_testExtensionKindFilter(t, factory) })
	t.Run("event body round-trips", func(t *testing.T) { runtimeContract_testEventBodyRoundTrip(t, factory, makeMessage) })
}

func runtimeContract_runSerializerConformanceTests[M adk.MessageType](t *testing.T, factory func(testing.TB, schema.Serializer) adk.SessionEventStore[M], makeMessage func(content string) M) {
	t.Helper()
	t.Run("custom serializer is honored", func(t *testing.T) {
		serializer := &countingEventSerializer{inner: &schema.HumanReadableSerializer{}}
		store := factory(t, serializer)
		if store == nil {
			t.Fatalf("factory returned nil SessionEventStore")
		}

		ctx := context.Background()
		event := runtimeContract_messageEvent("custom-serializer-1", makeMessage("custom serializer"))
		runtimeContract_appendEvents(t, ctx, store, "s", event)
		if serializer.marshalCount == 0 {
			t.Fatalf("custom serializer Marshal was not called")
		}

		res, err := store.LoadEvents(ctx, "s", &adk.LoadSessionEventsRequest{})
		runtimeContract_requireNoError(t, err)
		if serializer.unmarshalCount == 0 {
			t.Fatalf("custom serializer Unmarshal was not called")
		}
		runtimeContract_requireEventsEqual(t, []*adk.SessionEvent[M]{event}, res.Events)
	})
}

func runtimeContract_testAppendAndForwardLoad[M adk.MessageType](t *testing.T, factory func(testing.TB) adk.SessionEventStore[M], makeMessage func(string) M) {
	store := runtimeContract_newStore(t, factory)
	ctx := context.Background()

	first := runtimeContract_messageEvent("e1", makeMessage("first"))
	second := runtimeContract_committedIdleEvent[M]("e2")
	third := runtimeContract_messageEvent("e3", makeMessage("third"))
	runtimeContract_appendEvents(t, ctx, store, "s", first, second)
	runtimeContract_appendEvents(t, ctx, store, "s", third)

	res, err := store.LoadEvents(ctx, "s", &adk.LoadSessionEventsRequest{})
	runtimeContract_requireNoError(t, err)
	if res == nil {
		t.Fatalf("LoadEvents returned nil result")
	}
	runtimeContract_requireEventsEqual(t, []*adk.SessionEvent[M]{first, second, third}, res.Events)
}

func runtimeContract_testExtensionKindFilter[M adk.MessageType](t *testing.T, factory func(testing.TB) adk.SessionEventStore[M]) {
	store := runtimeContract_newStore(t, factory)
	ctx := context.Background()

	first := runtimeContract_extensionEvent[M]("custom-1", "x.conformance.custom")
	second := runtimeContract_committedIdleEvent[M]("turn-1")
	third := runtimeContract_extensionEvent[M]("custom-2", "x.conformance.custom")
	runtimeContract_appendEvents(t, ctx, store, "s", first, second, third)

	res, err := store.LoadEvents(ctx, "s", &adk.LoadSessionEventsRequest{
		Kinds: []adk.SessionEventKind{adk.SessionEventKind("x.conformance.custom")},
	})
	runtimeContract_requireNoError(t, err)
	if res == nil {
		t.Fatalf("LoadEvents returned nil result")
	}
	runtimeContract_requireEventsEqual(t, []*adk.SessionEvent[M]{first, third}, res.Events)
}

func runtimeContract_testReversePagination[M adk.MessageType](t *testing.T, factory func(testing.TB) adk.SessionEventStore[M], makeMessage func(string) M) {
	store := runtimeContract_newStore(t, factory)
	ctx := context.Background()

	events := make([]*adk.SessionEvent[M], 5)
	for i := 0; i < 5; i++ {
		events[i] = runtimeContract_messageEvent(fmt.Sprintf("r%d", i), makeMessage(fmt.Sprintf("%c", 'a'+i)))
		runtimeContract_appendEvents(t, ctx, store, "s", events[i])
	}

	var collected []string
	var after string
	for {
		res, err := store.LoadEvents(ctx, "s", &adk.LoadSessionEventsRequest{
			Reverse: true,
			Limit:   2,
			After:   after,
		})
		runtimeContract_requireNoError(t, err)
		if res == nil || len(res.Events) == 0 {
			break
		}
		for _, ep := range res.Events {
			collected = append(collected, ep.EventID)
		}
		if res.Next == "" {
			break
		}
		after = res.Next
	}

	expected := []string{"r4", "r3", "r2", "r1", "r0"}
	if len(collected) != len(expected) {
		t.Fatalf("reverse collected length=%d want=%d (got=%v)", len(collected), len(expected), collected)
	}
	for i := range expected {
		if collected[i] != expected[i] {
			t.Fatalf("reverse[%d]=%q want=%q (got=%v)", i, collected[i], expected[i], collected)
		}
	}
}

func runtimeContract_testForwardPagination[M adk.MessageType](t *testing.T, factory func(testing.TB) adk.SessionEventStore[M], makeMessage func(string) M) {
	store := runtimeContract_newStore(t, factory)
	ctx := context.Background()

	for i := 0; i < 80; i++ {
		event := runtimeContract_messageEvent(fmt.Sprintf("f%d", i), makeMessage(fmt.Sprintf("%d", i)))
		runtimeContract_appendEvents(t, ctx, store, "s", event)
	}

	var collected []*adk.SessionEvent[M]
	req := &adk.LoadSessionEventsRequest{Limit: 10}
	for {
		res, err := store.LoadEvents(ctx, "s", req)
		runtimeContract_requireNoError(t, err)
		if res == nil || len(res.Events) == 0 {
			break
		}
		collected = append(collected, res.Events...)
		if res.Next == "" {
			break
		}
		req = &adk.LoadSessionEventsRequest{Limit: 10, After: res.Next}
	}
	if len(collected) != 80 {
		t.Fatalf("expected 80 events, got %d", len(collected))
	}
	for i, ep := range collected {
		expectedID := fmt.Sprintf("f%d", i)
		if ep.EventID != expectedID {
			t.Fatalf("event[%d].EventID=%q, want=%q", i, ep.EventID, expectedID)
		}
	}
}

func runtimeContract_testSessionIsolation[M adk.MessageType](t *testing.T, factory func(testing.TB) adk.SessionEventStore[M], makeMessage func(string) M) {
	store := runtimeContract_newStore(t, factory)
	ctx := context.Background()

	alpha := runtimeContract_messageEvent("alpha-1", makeMessage("alpha"))
	beta := runtimeContract_committedIdleEvent[M]("beta-1")
	runtimeContract_appendEvents(t, ctx, store, "alpha", alpha)
	runtimeContract_appendEvents(t, ctx, store, "beta", beta)

	alphaRes, err := store.LoadEvents(ctx, "alpha", &adk.LoadSessionEventsRequest{})
	runtimeContract_requireNoError(t, err)
	runtimeContract_requireEventsEqual(t, []*adk.SessionEvent[M]{alpha}, alphaRes.Events)

	betaRes, err := store.LoadEvents(ctx, "beta", &adk.LoadSessionEventsRequest{})
	runtimeContract_requireNoError(t, err)
	runtimeContract_requireEventsEqual(t, []*adk.SessionEvent[M]{beta}, betaRes.Events)
}

func runtimeContract_testEmptySession[M adk.MessageType](t *testing.T, factory func(testing.TB) adk.SessionEventStore[M]) {
	store := runtimeContract_newStore(t, factory)
	ctx := context.Background()

	res, err := store.LoadEvents(ctx, "nonexistent", &adk.LoadSessionEventsRequest{})
	runtimeContract_requireNoError(t, err)
	if res != nil && len(res.Events) != 0 {
		t.Fatalf("expected empty result for nonexistent session, got %d events", len(res.Events))
	}
}

func runtimeContract_testRejectDuplicateEventID[M adk.MessageType](t *testing.T, factory func(testing.TB) adk.SessionEventStore[M], makeMessage func(string) M) {
	store := runtimeContract_newStore(t, factory)
	ctx := context.Background()

	first := runtimeContract_messageEvent("dup-1", makeMessage("first"))
	dup := runtimeContract_messageEvent("dup-1", makeMessage("second"))
	runtimeContract_appendEvents(t, ctx, store, "s", first)
	err := store.AppendEvents(ctx, "s", []*adk.SessionEvent[M]{dup})
	if !errors.Is(err, adk.ErrDuplicateEventID) {
		t.Fatalf("expected ErrDuplicateEventID, got %v", err)
	}

	res, err := store.LoadEvents(ctx, "s", &adk.LoadSessionEventsRequest{})
	runtimeContract_requireNoError(t, err)
	runtimeContract_requireEventsEqual(t, []*adk.SessionEvent[M]{first}, res.Events)
}

func runtimeContract_testRejectDuplicateEventIDWithinBatch[M adk.MessageType](t *testing.T, factory func(testing.TB) adk.SessionEventStore[M], makeMessage func(string) M) {
	store := runtimeContract_newStore(t, factory)
	ctx := context.Background()

	first := runtimeContract_messageEvent("dup-batch-1", makeMessage("first"))
	dup := runtimeContract_messageEvent("dup-batch-1", makeMessage("second"))
	err := store.AppendEvents(ctx, "s", []*adk.SessionEvent[M]{first, dup})
	if !errors.Is(err, adk.ErrDuplicateEventID) {
		t.Fatalf("expected ErrDuplicateEventID, got %v", err)
	}

	res, err := store.LoadEvents(ctx, "s", &adk.LoadSessionEventsRequest{})
	runtimeContract_requireNoError(t, err)
	runtimeContract_requireEventsEqual(t, nil, res.Events)
}

func runtimeContract_testRejectEmptyEventID[M adk.MessageType](t *testing.T, factory func(testing.TB) adk.SessionEventStore[M], makeMessage func(string) M) {
	store := runtimeContract_newStore(t, factory)
	ctx := context.Background()

	err := store.AppendEvents(ctx, "s", []*adk.SessionEvent[M]{{Kind: adk.SessionEventMessage, Message: makeMessage("empty")}})
	if !errors.Is(err, adk.ErrInvalidEventID) {
		t.Fatalf("expected ErrInvalidEventID, got %v", err)
	}
}

func runtimeContract_testAfterForward[M adk.MessageType](t *testing.T, factory func(testing.TB) adk.SessionEventStore[M], makeMessage func(string) M) {
	store := runtimeContract_newStore(t, factory)
	ctx := context.Background()

	events := make([]*adk.SessionEvent[M], 5)
	for i := 0; i < 5; i++ {
		events[i] = runtimeContract_messageEvent(fmt.Sprintf("fwd-%d", i), makeMessage(fmt.Sprintf("%d", i)))
		runtimeContract_appendEvents(t, ctx, store, "s", events[i])
	}

	res, err := store.LoadEvents(ctx, "s", &adk.LoadSessionEventsRequest{After: "fwd-2"})
	runtimeContract_requireNoError(t, err)
	runtimeContract_requireEventsEqual(t, []*adk.SessionEvent[M]{events[3], events[4]}, res.Events)
}

func runtimeContract_testAfterReverse[M adk.MessageType](t *testing.T, factory func(testing.TB) adk.SessionEventStore[M], makeMessage func(string) M) {
	store := runtimeContract_newStore(t, factory)
	ctx := context.Background()

	events := make([]*adk.SessionEvent[M], 5)
	for i := 0; i < 5; i++ {
		events[i] = runtimeContract_messageEvent(fmt.Sprintf("rev-%d", i), makeMessage(fmt.Sprintf("%d", i)))
		runtimeContract_appendEvents(t, ctx, store, "s", events[i])
	}

	res, err := store.LoadEvents(ctx, "s", &adk.LoadSessionEventsRequest{Reverse: true, After: "rev-2"})
	runtimeContract_requireNoError(t, err)
	runtimeContract_requireEventsEqual(t, []*adk.SessionEvent[M]{events[1], events[0]}, res.Events)
}

func runtimeContract_testUnknownAfter[M adk.MessageType](t *testing.T, factory func(testing.TB) adk.SessionEventStore[M], makeMessage func(string) M) {
	store := runtimeContract_newStore(t, factory)
	ctx := context.Background()

	runtimeContract_appendEvents(t, ctx, store, "s", runtimeContract_messageEvent("only-1", makeMessage("only")))

	_, err := store.LoadEvents(ctx, "s", &adk.LoadSessionEventsRequest{After: "ghost"})
	if !errors.Is(err, adk.ErrEventIDOutOfRange) {
		t.Fatalf("forward unknown After expected ErrEventIDOutOfRange, got %v", err)
	}
	_, err = store.LoadEvents(ctx, "s", &adk.LoadSessionEventsRequest{After: "ghost", Reverse: true})
	if !errors.Is(err, adk.ErrEventIDOutOfRange) {
		t.Fatalf("reverse unknown After expected ErrEventIDOutOfRange, got %v", err)
	}
}

func runtimeContract_testEmptyPageBoundary[M adk.MessageType](t *testing.T, factory func(testing.TB) adk.SessionEventStore[M], makeMessage func(string) M) {
	store := runtimeContract_newStore(t, factory)
	ctx := context.Background()

	ids := []string{"e0", "e1", "e2"}
	for _, id := range ids {
		runtimeContract_appendEvents(t, ctx, store, "s", runtimeContract_messageEvent(id, makeMessage(id)))
	}

	res, err := store.LoadEvents(ctx, "s", &adk.LoadSessionEventsRequest{After: "e2"})
	runtimeContract_requireNoError(t, err)
	if res == nil || len(res.Events) != 0 || res.Next != "" {
		t.Fatalf("forward empty page expected, got events=%d next=%q", len(res.Events), res.Next)
	}

	res, err = store.LoadEvents(ctx, "s", &adk.LoadSessionEventsRequest{Reverse: true, After: "e0"})
	runtimeContract_requireNoError(t, err)
	if res == nil || len(res.Events) != 0 || res.Next != "" {
		t.Fatalf("reverse empty page expected, got events=%d next=%q", len(res.Events), res.Next)
	}
}

func runtimeContract_testEventBodyRoundTrip[M adk.MessageType](t *testing.T, factory func(testing.TB) adk.SessionEventStore[M], makeMessage func(string) M) {
	store := runtimeContract_newStore(t, factory)
	ctx := context.Background()

	event := runtimeContract_messageEvent("body-test-1", makeMessage("body"))
	event.TurnID = "turn-body-test-1"
	runtimeContract_appendEvents(t, ctx, store, "s", event)

	res, err := store.LoadEvents(ctx, "s", &adk.LoadSessionEventsRequest{})
	runtimeContract_requireNoError(t, err)
	if res == nil || len(res.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(res.Events))
	}
	runtimeContract_requireEventsEqual(t, []*adk.SessionEvent[M]{event}, res.Events)
}

func runtimeContract_newStore[M adk.MessageType](t testing.TB, factory func(testing.TB) adk.SessionEventStore[M]) adk.SessionEventStore[M] {
	t.Helper()
	store := factory(t)
	if store == nil {
		t.Fatalf("factory returned nil SessionEventStore")
	}
	return store
}

func runtimeContract_appendEvents[M adk.MessageType](t testing.TB, ctx context.Context, store adk.SessionEventStore[M], sessionID string, events ...*adk.SessionEvent[M]) {
	t.Helper()
	err := store.AppendEvents(ctx, sessionID, events)
	runtimeContract_requireNoError(t, err)
}

func runtimeContract_requireNoError(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func runtimeContract_requireEventsEqual[M adk.MessageType](t testing.TB, want, got []*adk.SessionEvent[M]) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("events length mismatch: got=%d want=%d", len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("event[%d] mismatch:\n got: %#v\nwant: %#v", i, got[i], want[i])
		}
	}
}

type countingEventSerializer struct {
	inner          schema.Serializer
	marshalCount   int
	unmarshalCount int
}

func (s *countingEventSerializer) Marshal(v any) ([]byte, error) {
	s.marshalCount++
	return s.inner.Marshal(v)
}

func (s *countingEventSerializer) Unmarshal(data []byte, v any) error {
	s.unmarshalCount++
	return s.inner.Unmarshal(data, v)
}

func runtimeContract_messageEvent[M adk.MessageType](id string, msg M) *adk.SessionEvent[M] {
	return &adk.SessionEvent[M]{EventID: id, Kind: adk.SessionEventMessage, Message: msg}
}

func runtimeContract_committedIdleEvent[M adk.MessageType](id string) *adk.SessionEvent[M] {
	return &adk.SessionEvent[M]{
		EventID: id,
		Kind:    adk.SessionEventSessionStatusIdle,
		Lifecycle: &adk.LifecycleEvent{
			State:      adk.SessionRunStateIdle,
			StopReason: &adk.StopReason{Type: adk.StopReasonEndTurn},
		},
	}
}

func runtimeContract_extensionEvent[M adk.MessageType](id, kind string) *adk.SessionEvent[M] {
	return &adk.SessionEvent[M]{
		EventID: id,
		Kind:    adk.SessionEventKind(kind),
		Extension: &adk.SessionExtensionEvent{
			Data: &conformanceExtensionPayload{OK: true},
		},
	}
}

func TestRuntimeSessionOfficialContract(t *testing.T) {
	runtimeContract_runConformanceTests[*schema.Message](t, func(tb testing.TB) adk.SessionEventStore[*schema.Message] {
		s, _ := testRuntimeStore(tb, filepath.Join(tb.TempDir(), "contract.db"))
		return s
	}, schema.UserMessage)
}
