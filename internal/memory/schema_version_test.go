package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"agentgo/internal/db"
)

func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func userVersionOf(t *testing.T, conn *sql.DB) int {
	t.Helper()
	var v int
	if err := conn.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestANewDatabaseIsStampedWithTheCurrentSchemaGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentgo.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.DB().Close()
	if got := userVersionOf(t, rawDB(t, path)); got != db.SchemaVersion {
		t.Fatalf("user_version = %d, want %d", got, db.SchemaVersion)
	}
}

func TestADatabaseFromANewerBuildIsRefusedAndLeftExactlyAsItWas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentgo.db")
	newer := db.SchemaVersion + 3
	seed := rawDB(t, path)
	for _, stmt := range []string{
		`CREATE TABLE written_by_a_newer_build(x INTEGER)`,
		`INSERT INTO written_by_a_newer_build VALUES (42)`,
		fmt.Sprintf(`PRAGMA user_version=%d`, newer),
	} {
		if _, err := seed.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	_ = seed.Close()

	s, err := NewSQLiteStore(path)
	if err == nil {
		s.DB().Close()
		t.Fatal("a database written by a newer build must be refused")
	}
	var tooNew *db.SchemaTooNewError
	if !errors.As(err, &tooNew) || tooNew.Found != newer || tooNew.Supported != db.SchemaVersion {
		t.Fatalf("error = %v, want a SchemaTooNewError for generation %d", err, newer)
	}

	check := rawDB(t, path)
	if got := userVersionOf(t, check); got != newer {
		t.Fatalf("the refused database was re-stamped: user_version = %d, want %d", got, newer)
	}
	var tables int
	if err := check.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('memories','memory_links')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("the refused database got %d of our tables created in it", tables)
	}
	var x int
	if err := check.QueryRow(`SELECT x FROM written_by_a_newer_build`).Scan(&x); err != nil || x != 42 {
		t.Fatalf("the newer build's data was disturbed: %d %v", x, err)
	}
	var journal string
	if err := check.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil || journal != "delete" {
		t.Fatalf("even the journal mode was changed on a refused database: %q %v", journal, err)
	}
}

func TestADatabaseFromBeforeTheGuardIsAdoptedWithItsData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agentgo.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(ctx, Record{ID: "keep", Scope: "s", Content: "written before the guard existed", Modality: "fact", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	s.DB().Close()

	// Every build before the guard left user_version at 0.
	old := rawDB(t, path)
	if _, err := old.Exec(`PRAGMA user_version=0`); err != nil {
		t.Fatal(err)
	}
	_ = old.Close()

	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("an unversioned database must still open: %v", err)
	}
	var content string
	if err := s.DB().QueryRow(`SELECT content FROM memories WHERE id='keep'`).Scan(&content); err != nil || content == "" {
		t.Fatalf("data lost on adoption: %q %v", content, err)
	}
	s.DB().Close()
	if got := userVersionOf(t, rawDB(t, path)); got != db.SchemaVersion {
		t.Fatalf("user_version after adoption = %d, want %d", got, db.SchemaVersion)
	}
}
