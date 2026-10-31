package memory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// answerFor builds the JSON a well-behaved model returns for the children in a request.
func answerFor(t *testing.T, user, summary string) string {
	t.Helper()
	var in []struct {
		ID string `json:"id"`
	}
	// A retry appends a note after the JSON array.
	if err := json.Unmarshal([]byte(strings.SplitN(user, "\n\n", 2)[0]), &in); err != nil {
		t.Fatalf("request is not the children array: %v\n%s", err, user)
	}
	ids := make([]string, 0, len(in))
	for _, c := range in {
		ids = append(ids, c.ID)
	}
	raw, _ := json.Marshal(map[string]any{"summary": summary, "covered_children": ids})
	return string(raw)
}

func oneBranchStore(t *testing.T) (*SummaryForest, *SQLiteStore) {
	s := seedTreeStore(t, "s", map[string]int{"甲": 4})
	return NewSummaryForest(s), s
}

// A dense node whose answer is right but over the limit gets one request to shorten it,
// instead of losing the abstract (and every ancestor that waits for it).
func TestTooLongAbstractIsAskedToShortenOnce(t *testing.T) {
	ctx := context.Background()
	f, _ := oneBranchStore(t)
	var requests []string
	caller := func(_ context.Context, system, user string) (string, error) {
		requests = append(requests, user)
		if len(requests) == 1 {
			return answerFor(t, user, strings.Repeat("甲类记忆很长", 60)), nil
		}
		return answerFor(t, user, "甲类事实的简短概括"), nil
	}
	if err := f.Refresh(ctx, "s", caller); err != nil {
		t.Fatal(err)
	}
	ov, _ := f.Overview(ctx, "s")
	if len(requests) != 2 || ov.Branches != 1 || ov.Summarized != 1 {
		t.Fatalf("%d requests, %d of %d branches model-written", len(requests), ov.Summarized, ov.Branches)
	}
	if !strings.Contains(requests[1], "estimated tokens") || !strings.Contains(requests[1], "limit is 128") {
		t.Fatalf("the retry does not say how far over the limit the answer was:\n%s", requests[1])
	}
	if ov.Root == nil || ov.Root.Summary != "甲类事实的简短概括" || !ov.Root.Complete {
		t.Fatalf("root after retry: %+v", ov.Root)
	}
}

// An answer that stays too long after the one retry is dropped: the excerpt stays, nothing fails,
// and a node costs at most two calls.
func TestAbstractStillTooLongKeepsTheExcerptAndStopsAsking(t *testing.T) {
	ctx := context.Background()
	f, _ := oneBranchStore(t)
	calls := 0
	caller := func(_ context.Context, _, user string) (string, error) {
		calls++
		return answerFor(t, user, strings.Repeat("甲类记忆很长", 60)), nil
	}
	if err := f.Refresh(ctx, "s", caller); err != nil {
		t.Fatal(err)
	}
	ov, _ := f.Overview(ctx, "s")
	if calls != 2 || ov.Summarized != 0 || ov.Root == nil || ov.Root.Complete || !strings.Contains(ov.Root.Summary, "fact number") {
		t.Fatalf("calls=%d summarized=%d root=%+v", calls, ov.Summarized, ov.Root)
	}
}

// If the shortened answer is still a little long, it is cut back to its last whole clause rather
// than thrown away, and what is kept is a prefix of what the model wrote.
func TestStillLongAbstractIsCutAtAClauseBoundary(t *testing.T) {
	ctx := context.Background()
	f, _ := oneBranchStore(t)
	var clauses []string
	for i := 0; i < 18; i++ { // about 1.5 times the limit: worth cutting, not worth discarding
		clauses = append(clauses, "第"+strings.Repeat("甲", 5)+"条要点")
	}
	long := strings.Join(clauses, "；") + "。"
	calls := 0
	caller := func(_ context.Context, _, user string) (string, error) {
		calls++
		return answerFor(t, user, long), nil
	}
	if err := f.Refresh(ctx, "s", caller); err != nil {
		t.Fatal(err)
	}
	ov, _ := f.Overview(ctx, "s")
	if calls != 2 || ov.Summarized != 1 || ov.Root == nil || !ov.Root.Complete {
		t.Fatalf("calls=%d summarized=%d root=%+v", calls, ov.Summarized, ov.Root)
	}
	got := ov.Root.Summary
	if !strings.HasPrefix(long, got) || len(got) >= len(long) || !strings.HasSuffix(got, "；") {
		t.Fatalf("not a whole-clause prefix of the answer: %q", got)
	}
}

func TestFitToLimit(t *testing.T) {
	short := "只有一句话。"
	if got, ok := fitToLimit(short, 128); !ok || got != short {
		t.Fatalf("a fitting text must be kept as is: %q %v", got, ok)
	}
	long := strings.Repeat("这一条要点比较长；", 19)
	got, ok := fitToLimit(long, 128)
	if !ok || !strings.HasSuffix(got, "；") || !strings.HasPrefix(long, got) {
		t.Fatalf("cut: %q %v", got, ok)
	}
	if n := len([]rune(got)); n < 60 || n > 140 {
		t.Fatalf("kept %d runes of the limit's worth", n)
	}
	// Twice the limit or more would lose over half: the excerpt is the better stand-in.
	if _, ok = fitToLimit(strings.Repeat("这一条要点比较长；", 40), 128); ok {
		t.Fatal("an answer far over the limit was cut instead of refused")
	}
	if _, ok = fitToLimit(strings.Repeat("没有任何停顿的很长一段话", 40), 128); ok {
		t.Fatal("no boundary: nothing to cut at")
	}
	// A decimal point is not a sentence end, and a stump of under half is refused.
	if _, ok = fitToLimit("增长3.5个百分点"+strings.Repeat("再多一些没有停顿的字", 60), 128); ok {
		t.Fatal("a decimal point was taken for a sentence end")
	}
	if _, ok = fitToLimit("短。"+strings.Repeat("没有任何停顿的很长一段话", 40), 128); ok {
		t.Fatal("a stump of a few characters must not replace the excerpt")
	}
}

// A wrong answer (not covering the children) is not worth a second try, and no model at all is
// not an error: the caller returns nothing and the tree simply stays structural.
func TestWrongOrMissingAnswersAreNotRetried(t *testing.T) {
	ctx := context.Background()
	f, _ := oneBranchStore(t)
	calls := 0
	wrong := func(context.Context, string, string) (string, error) {
		calls++
		return `{"summary":"fabricated","covered_children":["foreign"]}`, nil
	}
	if err := f.Refresh(ctx, "s", wrong); err != nil {
		t.Fatal(err)
	}
	if ov, _ := f.Overview(ctx, "s"); calls != 1 || ov.Summarized != 0 {
		t.Fatalf("wrong answer: calls=%d summarized=%d", calls, ov.Summarized)
	}

	f, _ = oneBranchStore(t)
	calls = 0
	none := func(context.Context, string, string) (string, error) { calls++; return "", nil }
	if err := f.Refresh(ctx, "s", none); err != nil {
		t.Fatal(err)
	}
	if ov, _ := f.Overview(ctx, "s"); calls != 1 || ov.Summarized != 0 || !ov.Ready {
		t.Fatalf("no model: calls=%d summarized=%d ready=%v", calls, ov.Summarized, ov.Ready)
	}
}

func TestRejectionNoteNamesEveryReason(t *testing.T) {
	note := rejectionNote(map[string]int{rejectLong: 2, rejectCall: 1})
	if !strings.Contains(note, rejectLong+" ×2") || !strings.Contains(note, rejectCall+" ×1") || strings.Contains(note, rejectParse) {
		t.Fatalf("note: %q", note)
	}
	if rejectionNote(nil) != "" {
		t.Fatal("nothing rejected must say nothing")
	}
}
