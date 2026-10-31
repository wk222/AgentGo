package agent

import (
	"agentgo/internal/sessions"
	"context"
	"database/sql"
	"errors"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"path/filepath"
	"strings"
	"testing"
)

func testRuntimeStore(t testing.TB, file string) (*runtimeSessionStore, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	s, err := sessions.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	return newRuntimeSessionStore(s, t.TempDir()), db
}
func TestRuntimeSessionTransactionsAndPagination(t *testing.T) {
	ctx := context.Background()
	s, _ := testRuntimeStore(t, filepath.Join(t.TempDir(), "events.db"))
	event := func(id string) *adk.SessionEvent[*schema.Message] {
		return &adk.SessionEvent[*schema.Message]{EventID: id, Kind: adk.SessionEventMessage, Message: schema.UserMessage(id)}
	}
	if err := s.AppendEvents(ctx, "chat", []*adk.SessionEvent[*schema.Message]{event("a"), event("b"), event("c")}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvents(ctx, "chat", []*adk.SessionEvent[*schema.Message]{event("d"), event("b")}); !errors.Is(err, adk.ErrDuplicateEventID) {
		t.Fatalf("duplicate: %v", err)
	}
	got, err := s.LoadEvents(ctx, "chat", &adk.LoadSessionEventsRequest{Reverse: true, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 2 || got.Events[0].EventID != "c" || got.Next != "b" {
		t.Fatalf("reverse: %+v", got)
	}
	got, err = s.LoadEvents(ctx, "chat", &adk.LoadSessionEventsRequest{Reverse: true, After: got.Next})
	if err != nil || len(got.Events) != 1 || got.Events[0].EventID != "a" {
		t.Fatalf("cursor: %+v %v", got, err)
	}
	got, err = s.LoadEvents(ctx, "chat", nil)
	if err != nil || len(got.Events) != 3 {
		t.Fatalf("rollback failed: %+v %v", got, err)
	}
	if _, err = s.LoadEvents(ctx, "chat", &adk.LoadSessionEventsRequest{After: "missing"}); !errors.Is(err, adk.ErrEventIDOutOfRange) {
		t.Fatal(err)
	}
	got, err = s.LoadEvents(ctx, "other", nil)
	if err != nil || len(got.Events) != 0 {
		t.Fatal("session isolation", err)
	}
}
func TestRuntimeSessionBootstrapOnceAndReopen(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "events.db")
	s, db := testRuntimeStore(t, file)
	if err := s.bootstrap(ctx, "chat", []*schema.Message{schema.UserMessage("original")}); err != nil {
		t.Fatal(err)
	}
	if err := s.bootstrap(ctx, "chat", []*schema.Message{schema.UserMessage("duplicate")}); err != nil {
		t.Fatal(err)
	}
	scope := s.scope
	db.Close()
	restored, _ := testRuntimeStore(t, file)
	restored.scope = scope
	got, err := restored.LoadEvents(ctx, "chat", nil)
	if err != nil || len(got.Events) != 1 {
		t.Fatalf("reopen: %+v %v", got, err)
	}
	msgs := *got.Events[0].MessagesReplaced
	if len(msgs) != 1 || msgs[0].Content != "original" {
		t.Fatalf("history imported twice: %+v", msgs)
	}
}

type runtimeCaptureModel struct{ inputs [][]*schema.Message }

func (m *runtimeCaptureModel) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.inputs = append(m.inputs, append([]*schema.Message(nil), in...))
	return schema.AssistantMessage("answer", nil), nil
}
func (m *runtimeCaptureModel) Stream(ctx context.Context, in []*schema.Message, o ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, in, o...)
	return schema.StreamReaderFromArray([]*schema.Message{msg}), err
}
func (m *runtimeCaptureModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
func TestRuntimeSessionRunnerReplaysHistoryOnce(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "runner.db")
	store, db := testRuntimeStore(t, file)
	scope := store.scope
	if err := store.bootstrap(ctx, "chat", []*schema.Message{schema.SystemMessage("ORDERED_SYSTEM_A"), schema.SystemMessage("ORDERED_SYSTEM_B"), schema.UserMessage("legacy")}); err != nil {
		t.Fatal(err)
	}
	m := &runtimeCaptureModel{}
	runTurn := func(s *runtimeSessionStore, text string) {
		ag, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "test", Description: "test", Model: m, Instruction: "base", Handlers: []adk.ChatModelAgentMiddleware{NewSessionSpineMiddleware(SessionSpineConfig{WorkspaceRoot: t.TempDir(), MaxActiveTurns: 30})}})
		if err != nil {
			t.Fatal(err)
		}
		it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: ag, SessionID: "chat", SessionStore: s}).Query(ctx, text)
		for {
			e, ok := it.Next()
			if !ok {
				break
			}
			if e.Err != nil {
				t.Fatal(e.Err)
			}
		}
	}
	runTurn(store, "first")
	db.Close()
	restored, _ := testRuntimeStore(t, file)
	restored.scope = scope
	runTurn(restored, "second")
	if len(m.inputs) != 2 {
		t.Fatal(len(m.inputs))
	}
	var systemText strings.Builder
	for _, msg := range m.inputs[1] {
		if msg.Role == schema.System {
			systemText.WriteString(msg.Content)
		}
	}
	text := systemText.String()
	if strings.Count(text, "ORDERED_SYSTEM_A") != 1 || strings.Count(text, "ORDERED_SYSTEM_B") != 1 || strings.Index(text, "ORDERED_SYSTEM_A") > strings.Index(text, "ORDERED_SYSTEM_B") {
		t.Fatalf("caller system messages lost, repeated or reordered: %q", text)
	}
	counts := map[string]int{}
	for _, msg := range m.inputs[1] {
		counts[msg.Content]++
	}
	for _, text := range []string{"legacy", "first", "answer", "second"} {
		if counts[text] != 1 {
			t.Fatalf("replay duplicates or loses %q: %+v", text, counts)
		}
	}
}

type rejectBootstrapSerializer struct{}

func (*rejectBootstrapSerializer) Marshal(any) ([]byte, error) {
	return nil, errors.New("unexpected serialization of migrated history")
}
func (*rejectBootstrapSerializer) Unmarshal([]byte, any) error { return errors.New("unexpected read") }
func TestRuntimeSessionBootstrapSkipsLegacySerializationAfterMigration(t *testing.T) {
	s, _ := testRuntimeStore(t, filepath.Join(t.TempDir(), "fast.db"))
	ctx := context.Background()
	if err := s.bootstrap(ctx, "chat", nil); err != nil {
		t.Fatal(err)
	}
	s.serializer = &rejectBootstrapSerializer{}
	if err := s.bootstrap(ctx, "agentic:chat", []*schema.Message{schema.UserMessage("unneeded history")}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeMigrationLoadsBroaderHistoryOnceAndFailsOnReadError(t *testing.T) {
	s, _ := testRuntimeStore(t, filepath.Join(t.TempDir(), "migration.db"))
	ctx := context.Background()
	calls := 0
	ctx = WithMigrationHistory(ctx, func(context.Context) ([]*schema.Message, error) {
		calls++
		return []*schema.Message{schema.UserMessage("early goal"), schema.UserMessage("correction")}, nil
	})
	if err := s.bootstrap(ctx, "a", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.bootstrap(ctx, "a", nil); err != nil || calls != 1 {
		t.Fatal("migration source loaded repeatedly", calls, err)
	}
	failed := errors.New("legacy storage read failed")
	ctx = WithMigrationHistory(context.Background(), func(context.Context) ([]*schema.Message, error) { return nil, failed })
	if err := s.bootstrap(ctx, "b", nil); !errors.Is(err, failed) {
		t.Fatal("failed import became an empty session", err)
	}
	events, err := s.LoadEvents(context.Background(), "b", nil)
	if err != nil || len(events.Events) != 0 {
		t.Fatal("committed failed migration", err)
	}
}
