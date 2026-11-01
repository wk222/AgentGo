package sessions

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	agentdb "agentgo/internal/db"

	_ "modernc.org/sqlite"
)

// Two processes (two terminals running agentgo-cli) share one agentgo.db. Each
// boot appends its first context event in a transaction that reads MAX(seq) and
// then inserts. Under WAL that is a deferred transaction: when both read the
// same snapshot and one commits, the other's write fails with SQLITE_BUSY_
// SNAPSHOT (517), which busy_timeout does not retry. The run then degrades to a
// tool-less chat and the reply claims the task is unsupported.
func TestConcurrentBootstrapDoesNotFailWithBusySnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	open := func() *sql.DB {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if err := agentdb.Configure(db); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(db); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}

	// Several stores, like several agentgo-cli processes on the same data dir.
	const processes = 6
	stores := make([]*Store, processes)
	for i := range stores {
		db := open()
		s, err := Open(db)
		if err != nil {
			t.Fatal(err)
		}
		stores[i] = s
	}

	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make([]error, processes)
	start := make(chan struct{})
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for round := 0; round < 10; round++ {
				sid := "sess"
				scope := "eino-v1:shared"
				// Alternate bootstrap (first run of a session) and appends.
				bootstrap := round == 0
				rec := []ContextEventRecord{{ID: strings.Join([]string{sid, scope, string(rune('a' + i)), string(rune('0' + round))}, "-"), Kind: "message", Payload: []byte("x")}}
				if err := stores[i].AppendContextEvents(ctx, sid, scope, rec, bootstrap); err != nil {
					errs[i] = err
					return
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("process %d: %v", i, err)
		}
	}
}
