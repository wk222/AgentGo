package tools

import (
	"encoding/json"
	"errors"
	"sync"
)

// InteractionResult is submitted from the UI when the user interacts with an A2UI component.
type InteractionResult struct {
	InteractID string          `json:"interact_id"`
	Action     string          `json:"action"` // button clicked, form submitted, etc.
	Data       json.RawMessage `json:"data"`
}

// InteractionStore manages pending A2UI interactions using a channel-based wait/resolve pattern.
type InteractionStore struct {
	pending sync.Map // interact_id -> chan InteractionResult
}

// NewInteractionStore creates a new InteractionStore.
func NewInteractionStore() *InteractionStore {
	return &InteractionStore{}
}

// Register creates a channel for the given interaction ID and returns it.
// The caller should select on the returned channel (with a timeout) to wait for the result.
func (s *InteractionStore) Register(id string) <-chan InteractionResult {
	ch := make(chan InteractionResult, 1)
	s.pending.Store(id, ch)
	return ch
}

// Resolve sends the result to the waiting channel for the given interaction ID.
// Returns an error if the interaction ID is not found (already resolved or never registered).
func (s *InteractionStore) Resolve(id string, result InteractionResult) error {
	v, ok := s.pending.LoadAndDelete(id)
	if !ok {
		return errors.New("interaction not found: " + id)
	}
	ch := v.(chan InteractionResult)
	ch <- result
	return nil
}

// Cancel removes the pending interaction without sending a result, closing the channel.
func (s *InteractionStore) Cancel(id string) {
	v, ok := s.pending.LoadAndDelete(id)
	if !ok {
		return
	}
	ch := v.(chan InteractionResult)
	close(ch)
}
