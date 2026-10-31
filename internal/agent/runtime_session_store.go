package agent

import (
	"agentgo/internal/sessions"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"path/filepath"
	"strings"
	"time"
)

// runtimeSessionStore adapts the host's transactional SQLite storage to the
// official Eino event contract. Chat messages remain a UI transcript only.
type runtimeSessionStore struct {
	store      *sessions.Store
	scope      string
	serializer schema.Serializer
}

func newRuntimeSessionStore(store *sessions.Store, root string) *runtimeSessionStore {
	absolute, _ := filepath.Abs(root)
	if filepath.Separator == '\\' {
		absolute = strings.ToLower(absolute)
	}
	digest := sha256.Sum256([]byte(filepath.Clean(absolute)))
	return &runtimeSessionStore{store: store, scope: fmt.Sprintf("eino-v1:%x", digest[:16]), serializer: &schema.HumanReadableSerializer{}}
}

func (s *runtimeSessionStore) encode(events []*adk.SessionEvent[*schema.Message]) ([]sessions.ContextEventRecord, error) {
	records := make([]sessions.ContextEventRecord, 0, len(events))
	seen := map[string]bool{}
	for _, event := range events {
		if event == nil || event.EventID == "" {
			return nil, adk.ErrInvalidEventID
		}
		if seen[event.EventID] {
			return nil, adk.ErrDuplicateEventID
		}
		seen[event.EventID] = true
		copy := *event
		if err := adk.NormalizeSessionEventKind(&copy); err != nil {
			return nil, err
		}
		data, err := s.serializer.Marshal(&copy)
		if err != nil {
			return nil, err
		}
		records = append(records, sessions.ContextEventRecord{ID: copy.EventID, Kind: string(copy.Kind), Payload: data})
	}
	return records, nil
}
func (s *runtimeSessionStore) AppendEvents(ctx context.Context, sid string, events []*adk.SessionEvent[*schema.Message]) error {
	records, err := s.encode(events)
	if err != nil {
		return err
	}
	err = s.store.AppendContextEvents(ctx, strings.TrimPrefix(sid, "agentic:"), s.scope, records, false)
	if errors.Is(err, sessions.ErrContextEventExists) {
		return adk.ErrDuplicateEventID
	}
	return err
}
func (s *runtimeSessionStore) LoadEvents(ctx context.Context, sid string, req *adk.LoadSessionEventsRequest) (*adk.LoadSessionEventsResult[*schema.Message], error) {
	if req == nil {
		req = &adk.LoadSessionEventsRequest{}
	}
	kinds := make([]string, len(req.Kinds))
	for i, k := range req.Kinds {
		kinds[i] = string(k)
	}
	rows, more, err := s.store.LoadContextEvents(ctx, strings.TrimPrefix(sid, "agentic:"), s.scope, req.After, req.Reverse, kinds, req.Limit)
	if errors.Is(err, sessions.ErrContextCursor) {
		return nil, adk.ErrEventIDOutOfRange
	}
	if err != nil {
		return nil, err
	}
	result := &adk.LoadSessionEventsResult[*schema.Message]{}
	for _, row := range rows {
		var event adk.SessionEvent[*schema.Message]
		if err := s.serializer.Unmarshal(row.Payload, &event); err != nil {
			return nil, err
		}
		if event.EventID != row.ID || string(event.Kind) != row.Kind {
			return nil, errors.New("runtime event header mismatch")
		}
		result.Events = append(result.Events, &event)
	}
	if more && len(result.Events) > 0 {
		result.Next = result.Events[len(result.Events)-1].EventID
	}
	return result, nil
}

func (s *runtimeSessionStore) bootstrap(ctx context.Context, sid string, history []*schema.Message) error {
	exists, err := s.store.HasContextEvents(ctx, strings.TrimPrefix(sid, "agentic:"), s.scope)
	if err != nil || exists {
		return err
	}
	if history == nil {
		history, err = migrationHistoryFrom(ctx)
		if err != nil {
			return err
		}
	}
	// Conditional insert is atomic. IDs are assigned before the first replacement,
	// so later framework edits refer to durable identities.
	imported := make([]*schema.Message, 0, len(history))
	for _, m := range history {
		if m == nil {
			continue
		}
		copy := *m
		copy.Extra = map[string]any{}
		for k, v := range m.Extra {
			copy.Extra[k] = v
		}
		adk.EnsureMessageID(&copy)
		imported = append(imported, &copy)
	}
	event := &adk.SessionEvent[*schema.Message]{EventID: uuid.NewString(), Timestamp: time.Now(), Kind: adk.SessionEventMessagesReplaced, MessagesReplaced: &imported}
	records, err := s.encode([]*adk.SessionEvent[*schema.Message]{event})
	if err != nil {
		return err
	}
	return s.store.AppendContextEvents(ctx, strings.TrimPrefix(sid, "agentic:"), s.scope, records, true)
}

func (r *Runner) sessionRunnerConfig(ctx context.Context, sid string, agent adk.Agent, streaming bool, bootstrap bool) (adk.RunnerConfig, error) {
	cfg := adk.RunnerConfig{Agent: agent, CheckPointStore: r.cpStore, EnableStreaming: streaming}
	if r.runtimeSessions == nil || sid == "" {
		return cfg, nil
	}
	if !bootstrap {
		exists, err := r.runtimeSessions.store.HasContextEvents(ctx, strings.TrimPrefix(sid, "agentic:"), r.runtimeSessions.scope)
		if err != nil {
			return cfg, err
		}
		if !exists {
			return cfg, nil
		} // Legacy checkpoints retain their original history contract.
	}
	if bootstrap {
		if err := r.runtimeSessions.bootstrap(ctx, sid, nil); err != nil {
			return cfg, fmt.Errorf("initialize durable session: %w", err)
		}
	}
	cfg.SessionID, cfg.SessionStore = sid, r.runtimeSessions
	return cfg, nil
}

func (r *Runner) sessionRunMessages(ctx context.Context, sid, text string) []*schema.Message {
	if r.runtimeSessions != nil && sid != "" {
		return []*schema.Message{schema.UserMessage(text)}
	}
	return runMessages(ctx, text)
}
