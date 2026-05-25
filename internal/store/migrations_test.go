package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"
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
	// Version 0 (baseline) + one row per real migration.
	want := 1 + len(migrations)
	if count != want {
		t.Errorf("expected %d rows after idempotent run, got %d", want, count)
	}
}

func TestMigrateAppliesInOrder(t *testing.T) {
	// Use version numbers above the highest real migration to avoid conflicts.
	base := len(migrations) + 1
	var order []int
	ms := []migration{
		{version: base, description: "first", fn: func(tx *sql.Tx) error { order = append(order, base); return nil }},
		{version: base + 1, description: "second", fn: func(tx *sql.Tx) error { order = append(order, base+1); return nil }},
		{version: base + 2, description: "third", fn: func(tx *sql.Tx) error { order = append(order, base+2); return nil }},
	}
	s := openMem(t)
	if err := s.runMigrations(ms); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}
	if len(order) != 3 || order[0] != base || order[1] != base+1 || order[2] != base+2 {
		t.Errorf("wrong execution order: %v", order)
	}
	// Version 0 (baseline) + real migrations + 3 test migrations.
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count)
	want := 1 + len(migrations) + 3
	if count != want {
		t.Errorf("expected %d rows in schema_migrations, got %d", want, count)
	}
}

func TestMigrateSkipsAlreadyApplied(t *testing.T) {
	calls := 0
	// Use a version number above the highest real migration to avoid conflicts.
	next := len(migrations) + 1
	ms := []migration{
		{version: next, description: "once", fn: func(tx *sql.Tx) error { calls++; return nil }},
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

func TestMigrateBackfillSeriesTimestamps(t *testing.T) {
	// Build a bare DB with baseline stamped and migration 1 applied, but not 2.
	s := openBare(t, ":memory:")
	if _, err := s.db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := s.db.Exec(migrationsSchema); err != nil {
		t.Fatalf("create migrations schema: %v", err)
	}
	for _, v := range []int{0, 1} {
		if _, err := s.db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, '2026-01-01T00:00:00Z')`, v); err != nil {
			t.Fatalf("stamp v%d: %v", v, err)
		}
	}

	seedSQL := `INSERT INTO store (bucket, key, value) VALUES (?, ?, ?)`
	// Zero-timestamp records — should be updated.
	zeroRecords := []struct{ key, value string }{
		{"my show|S01E06", `{"series_name":"my show","episode_id":"S01E06","downloaded_at":"0001-01-01T00:00:00Z","quality":{}}`},
		{"my show|S01E07", `{"series_name":"my show","episode_id":"S01E07","downloaded_at":"0001-01-01T00:00:00Z","quality":{}}`},
	}
	// Real-timestamp record — must remain unchanged.
	realRecord := struct{ key, value string }{
		"my show|S01E01", `{"series_name":"my show","episode_id":"S01E01","downloaded_at":"2026-05-24T12:00:00Z","quality":{}}`,
	}
	for _, r := range zeroRecords {
		if _, err := s.db.Exec(seedSQL, "series", r.key, r.value); err != nil {
			t.Fatalf("seed %q: %v", r.key, err)
		}
	}
	if _, err := s.db.Exec(seedSQL, "series", realRecord.key, realRecord.value); err != nil {
		t.Fatalf("seed real record: %v", err)
	}

	before := time.Now()
	if err := s.runMigrations(migrations[1:2]); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}
	after := time.Now()

	// Zero-timestamp records must now have a non-zero timestamp within [before, after].
	for _, r := range zeroRecords {
		var val string
		if err := s.db.QueryRow(`SELECT value FROM store WHERE bucket='series' AND key=?`, r.key).Scan(&val); err != nil {
			t.Fatalf("read %q: %v", r.key, err)
		}
		var rec struct {
			DownloadedAt time.Time `json:"downloaded_at"`
		}
		if err := json.Unmarshal([]byte(val), &rec); err != nil {
			t.Fatalf("unmarshal %q: %v", r.key, err)
		}
		if rec.DownloadedAt.IsZero() {
			t.Errorf("%q: downloaded_at is still zero after migration", r.key)
		}
		if rec.DownloadedAt.Before(before) || rec.DownloadedAt.After(after) {
			t.Errorf("%q: downloaded_at %v not within migration window [%v, %v]", r.key, rec.DownloadedAt, before, after)
		}
	}

	// Real-timestamp record must not be touched.
	var val string
	if err := s.db.QueryRow(`SELECT value FROM store WHERE bucket='series' AND key=?`, realRecord.key).Scan(&val); err != nil {
		t.Fatalf("read real record: %v", err)
	}
	var rec struct {
		DownloadedAt string `json:"downloaded_at"`
	}
	if err := json.Unmarshal([]byte(val), &rec); err != nil {
		t.Fatalf("unmarshal real record: %v", err)
	}
	if rec.DownloadedAt != "2026-05-24T12:00:00Z" {
		t.Errorf("real record downloaded_at changed: got %q", rec.DownloadedAt)
	}
}

func TestMigrateNormalizeSeriesKeys(t *testing.T) {
	// Build a bare DB: schema present, baseline stamped, but migration 1 not yet applied.
	// Seeding happens before the migration runs so we can verify the transformation.
	s := openBare(t, ":memory:")
	if _, err := s.db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := s.db.Exec(migrationsSchema); err != nil {
		t.Fatalf("create migrations schema: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (0, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("stamp baseline: %v", err)
	}

	// Seed stale capitalized entries (pre-ac9e5c7 format).
	seedSQL := `INSERT INTO store (bucket, key, value) VALUES (?, ?, ?)`
	staleRecords := []struct {
		key   string
		value string
	}{
		// Old capitalized key with real timestamp — no normalized equivalent.
		{"The Testaments|S01E05", `{"series_name":"The Testaments","episode_id":"S01E05","downloaded_at":"2026-05-10T12:00:00Z","quality":{}}`},
		// Old capitalized key where a normalized entry already exists with a later timestamp.
		{"The Testaments|S01E01", `{"series_name":"The Testaments","episode_id":"S01E01","downloaded_at":"2026-05-08T00:00:00Z","quality":{}}`},
		// Old capitalized key where a normalized entry already exists with a zero timestamp (stale should win).
		{"Good Omens|S02E01", `{"series_name":"Good Omens","episode_id":"S02E01","downloaded_at":"2026-05-09T00:00:00Z","quality":{}}`},
	}
	normalized := []struct {
		key   string
		value string
	}{
		// Already-normalized entry with later timestamp — should survive unchanged.
		{"the testaments|S01E01", `{"series_name":"the testaments","display_name":"The Testaments","episode_id":"S01E01","downloaded_at":"2026-05-24T12:00:00Z","quality":{}}`},
		// Already-normalized entry with zero timestamp — stale capitalized should overwrite.
		{"good omens|S02E01", `{"series_name":"good omens","episode_id":"S02E01","downloaded_at":"0001-01-01T00:00:00Z","quality":{}}`},
	}
	for _, r := range staleRecords {
		if _, err := s.db.Exec(seedSQL, "series", r.key, r.value); err != nil {
			t.Fatalf("seed stale %q: %v", r.key, err)
		}
	}
	for _, r := range normalized {
		if _, err := s.db.Exec(seedSQL, "series", r.key, r.value); err != nil {
			t.Fatalf("seed normalized %q: %v", r.key, err)
		}
	}

	// Run only migration 1 (the normalize migration).
	if err := s.runMigrations(migrations[:1]); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	// Old capitalized keys must be gone.
	for _, r := range staleRecords {
		var dummy string
		err := s.db.QueryRow(`SELECT value FROM store WHERE bucket='series' AND key=?`, r.key).Scan(&dummy)
		if err != sql.ErrNoRows {
			t.Errorf("stale key %q still present after migration", r.key)
		}
	}

	// the testaments|S01E01: normalized entry had later timestamp — its value must be preserved.
	var val string
	if err := s.db.QueryRow(`SELECT value FROM store WHERE bucket='series' AND key='the testaments|S01E01'`).Scan(&val); err != nil {
		t.Fatalf("the testaments|S01E01 missing: %v", err)
	}
	var rec struct {
		SeriesName   string `json:"series_name"`
		DownloadedAt string `json:"downloaded_at"`
	}
	if err := json.Unmarshal([]byte(val), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec.SeriesName != "the testaments" {
		t.Errorf("series_name = %q, want %q", rec.SeriesName, "the testaments")
	}
	if rec.DownloadedAt != "2026-05-24T12:00:00Z" {
		t.Errorf("downloaded_at = %q, want newer timestamp kept", rec.DownloadedAt)
	}

	// the testaments|S01E05: did not exist before — must be created from stale entry.
	if err := s.db.QueryRow(`SELECT value FROM store WHERE bucket='series' AND key='the testaments|S01E05'`).Scan(&val); err != nil {
		t.Fatalf("the testaments|S01E05 missing: %v", err)
	}
	if err := json.Unmarshal([]byte(val), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec.SeriesName != "the testaments" {
		t.Errorf("series_name = %q, want %q", rec.SeriesName, "the testaments")
	}

	// good omens|S02E01: stale had later timestamp — must overwrite the zero-timestamp entry.
	if err := s.db.QueryRow(`SELECT value FROM store WHERE bucket='series' AND key='good omens|S02E01'`).Scan(&val); err != nil {
		t.Fatalf("good omens|S02E01 missing: %v", err)
	}
	if err := json.Unmarshal([]byte(val), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec.SeriesName != "good omens" {
		t.Errorf("series_name = %q, want %q", rec.SeriesName, "good omens")
	}
	if rec.DownloadedAt != "2026-05-09T00:00:00Z" {
		t.Errorf("downloaded_at = %q, want stale timestamp applied", rec.DownloadedAt)
	}
}
