package artifact

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memStore struct{ recs []Record }

func (m *memStore) Latest(_ context.Context, _ string, path string) (Record, bool, error) {
	for i := len(m.recs) - 1; i >= 0; i-- {
		if m.recs[i].Path == path {
			return m.recs[i], true, nil
		}
	}
	return Record{}, false, nil
}
func (m *memStore) Save(_ context.Context, _ string, r Record) error {
	m.recs = append(m.recs, r)
	return nil
}

func setup(t *testing.T) (context.Context, *memStore, string, string) {
	t.Helper()
	store, dl := &memStore{}, filepath.Join(t.TempDir(), "attachments")
	return WithStore(context.Background(), "s1", store, dl), store, dl, t.TempDir()
}

func write(t *testing.T, dir, name, text string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDeliverVersionsByContentAndKeepsTheDeliveredBytes(t *testing.T) {
	ctx, store, dl, dir := setup(t)
	p := write(t, dir, "report.csv", "a,b\n1,2\n")

	r1, err := Deliver(ctx, p, "报告")
	if err != nil || r1.Version != 1 || r1.Status != StatusNew || !r1.Attached || !strings.HasPrefix(r1.URL, "/attachments/artifact-") {
		t.Fatalf("first = %+v %v", r1, err)
	}
	// 同内容再交付：不新增。
	r1b, _ := Deliver(ctx, p, "")
	if r1b.Status != StatusUnchanged || r1b.Attached || len(store.recs) != 1 {
		t.Fatalf("again = %+v rows=%d", r1b, len(store.recs))
	}
	// 原地改写：新版本，旧版本的副本仍在。
	write(t, dir, "report.csv", "a,b\n1,3\n")
	r2, _ := Deliver(ctx, p, "")
	if r2.Version != 2 || r2.Status != StatusNewVersion || r2.Snapshot == r1.Snapshot {
		t.Fatalf("second = %+v", r2)
	}
	for _, r := range []Record{r1, r2} {
		if _, err := os.Stat(filepath.Join(dl, r.Snapshot)); err != nil {
			t.Fatalf("the delivered copy of v%d is gone: %v", r.Version, err)
		}
	}
	got, _ := os.ReadFile(filepath.Join(dl, r1.Snapshot))
	if string(got) != "a,b\n1,2\n" {
		t.Fatalf("v1 copy must keep the delivered bytes, got %q", got)
	}
}

func TestOnlyRealFilesAreDelivered(t *testing.T) {
	ctx, store, _, dir := setup(t)
	if _, err := Deliver(ctx, filepath.Join(dir, "never-written.xlsx"), ""); err == nil || len(store.recs) != 0 {
		t.Fatalf("a path that does not exist is not a delivery: %v rows=%d", err, len(store.recs))
	}
	if _, err := Deliver(ctx, dir, ""); err == nil {
		t.Fatal("a folder is not a file")
	}
	if _, err := Deliver(context.Background(), write(t, dir, "x.txt", "x"), ""); err != ErrNoSession {
		t.Fatalf("without a conversation: %v", err)
	}
}

func TestRemovedFileDoesNotComeBackUntilItsContentChanges(t *testing.T) {
	ctx, store, dl, dir := setup(t)
	p := write(t, dir, "out.txt", "v1")
	r1, _ := Deliver(ctx, p, "")
	if _, err := Tombstone(ctx, store, dl, "s1", r1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dl, r1.Snapshot)); err == nil {
		t.Fatal("the downloadable copy must be deleted")
	}
	again, _ := Deliver(ctx, p, "")
	if again.Status != StatusRemoved || again.Attached || len(store.recs) != 2 {
		t.Fatalf("a removed file must not resurrect: %+v rows=%d", again, len(store.recs))
	}
	write(t, dir, "out.txt", "v2 changed")
	r2, _ := Deliver(ctx, p, "")
	if r2.Version != 2 || !r2.Attached {
		t.Fatalf("changed content is a new version: %+v", r2)
	}
}

func TestSnapshotNameIsASafeFlatName(t *testing.T) {
	r := Record{SHA256: strings.Repeat("ab", 32), Version: 3, Name: `../we ird:na/me?.xlsx`}
	n := snapshotName(r)
	if strings.ContainsAny(n, `/\:?* `) || !strings.HasSuffix(n, ".xlsx") {
		t.Fatalf("name = %q", n)
	}
}
