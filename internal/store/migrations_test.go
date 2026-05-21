package store

import (
	"database/sql"
	"errors"
	"testing"
)

// openBare creates a SQLiteStore bypassing OpenSQLite (no lock, no migrate).
// Used to set up fixture databases in a controlled state before testing migrate.
func openBare(t *testing.T, path string) *SQLiteStore {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("openBare: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return &SQLiteStore{db: db}
}

func TestMigrateNewDB(t *testing.T) {
	s := openMem(t)
	var v int
	err := s.db.QueryRow(`SELECT version FROM schema_migrations WHERE version = 0`).Scan(&v)
	if err != nil {
		t.Fatalf("baseline not stamped on new DB: %v", err)
	}
}

func TestMigrateExistingDB(t *testing.T) {
	// Simulate a pre-migration DB: store table and data exist, but no schema_migrations.
	path := t.TempDir() + "/legacy.db"
	{
		s := openBare(t, path)
		if _, err := s.db.Exec(schema); err != nil {
			t.Fatalf("create legacy schema: %v", err)
		}
		if err := s.Bucket("b").Put("k", "hello"); err != nil {
			t.Fatalf("seed legacy data: %v", err)
		}
	}

	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("open legacy DB: %v", err)
	}
	defer s.Close()

	var v int
	if err := s.db.QueryRow(`SELECT version FROM schema_migrations WHERE version = 0`).Scan(&v); err != nil {
		t.Fatalf("baseline not stamped on legacy DB: %v", err)
	}

	var val string
	found, err := s.Bucket("b").Get("k", &val)
	if err != nil || !found || val != "hello" {
		t.Errorf("legacy data corrupted: found=%v err=%v val=%q", found, err, val)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	s := openMem(t)
	if err := s.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 row after idempotent run, got %d", count)
	}
}

func TestMigrateAppliesInOrder(t *testing.T) {
	var order []int
	ms := []migration{
		{version: 1, description: "first", fn: func(tx *sql.Tx) error { order = append(order, 1); return nil }},
		{version: 2, description: "second", fn: func(tx *sql.Tx) error { order = append(order, 2); return nil }},
		{version: 3, description: "third", fn: func(tx *sql.Tx) error { order = append(order, 3); return nil }},
	}
	s := openMem(t)
	if err := s.runMigrations(ms); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Errorf("wrong execution order: %v", order)
	}
	// Baseline (0) + 3 applied.
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count)
	if count != 4 {
		t.Errorf("expected 4 rows in schema_migrations, got %d", count)
	}
}

func TestMigrateSkipsAlreadyApplied(t *testing.T) {
	calls := 0
	ms := []migration{
		{version: 1, description: "once", fn: func(tx *sql.Tx) error { calls++; return nil }},
	}
	s := openMem(t)
	if err := s.runMigrations(ms); err != nil {
		t.Fatal(err)
	}
	if err := s.runMigrations(ms); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("migration called %d times, want 1", calls)
	}
}

func TestMigrateFailureRollsBack(t *testing.T) {
	errBoom := errors.New("boom")
	ms := []migration{
		{version: 1, description: "failing", fn: func(tx *sql.Tx) error {
			if _, err := tx.Exec(`CREATE TABLE should_not_exist (id INTEGER)`); err != nil {
				return err
			}
			return errBoom
		}},
	}

	path := t.TempDir() + "/fail.db"
	s := openBare(t, path)
	if _, err := s.db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	if err := s.runMigrations(ms); !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}

	// DDL inside the failed transaction must be rolled back.
	var name string
	err := s.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='should_not_exist'`,
	).Scan(&name)
	if err != sql.ErrNoRows {
		t.Errorf("rolled-back DDL should not persist: name=%q err=%v", name, err)
	}

	// Version 1 must not be recorded; version 0 (baseline) must be.
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&count)
	if count != 0 {
		t.Error("failed migration version must not be recorded")
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 0`).Scan(&count)
	if count != 1 {
		t.Error("baseline version 0 must remain after failed migration")
	}
}
