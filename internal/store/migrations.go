package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"slices"
	"time"
)

const migrationsSchema = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL
)
`

type migration struct {
	version     int
	description string
	fn          func(*sql.Tx) error
}

// migrations is the ordered list of all schema and data migrations.
// Never remove or reorder entries — only append.
// Version 0 is the baseline (the initial schema); it is never a runnable fn.
var migrations []migration

func init() {
	// Sort after all per-migration init() functions have run (this file sorts
	// after migration_*.go alphabetically, so this init executes last).
	slices.SortFunc(migrations, func(a, b migration) int {
		return a.version - b.version
	})
}

func (s *SQLiteStore) migrate() error {
	return s.runMigrations(migrations)
}

// runMigrations creates the schema_migrations table if needed, stamps version 0
// for new or pre-migration databases, then applies every pending migration in
// order. Each migration runs in its own transaction so failures never affect
// previously committed work.
func (s *SQLiteStore) runMigrations(pending []migration) error {
	if _, err := s.db.Exec(migrationsSchema); err != nil {
		return fmt.Errorf("store: create migrations table: %w", err)
	}

	var current int
	if err := s.db.QueryRow(
		`SELECT COALESCE(MAX(version), -1) FROM schema_migrations`,
	).Scan(&current); err != nil {
		return fmt.Errorf("store: query migration version: %w", err)
	}

	if current == -1 {
		// No migrations recorded: either a brand-new DB or a pre-migration
		// existing DB. In both cases the baseline schema is already in place.
		if _, err := s.db.Exec(
			`INSERT INTO schema_migrations (version, applied_at) VALUES (0, ?)`,
			time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			return fmt.Errorf("store: stamp baseline: %w", err)
		}
		current = 0
	}

	for _, m := range pending {
		if m.version <= current {
			continue
		}
		if err := s.applyMigration(m); err != nil {
			return err
		}
		current = m.version
	}
	return nil
}

func (s *SQLiteStore) applyMigration(m migration) (retErr error) {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: migration %d: begin: %w", m.version, err)
	}
	defer func() {
		if retErr != nil {
			_ = tx.Rollback()
		}
	}()

	if err := m.fn(tx); err != nil {
		return fmt.Errorf("store: migration %d (%s): %w", m.version, m.description, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		m.version, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("store: migration %d: record: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: migration %d: commit: %w", m.version, err)
	}
	slog.Info("store: applied migration", "version", m.version, "description", m.description)
	return nil
}
