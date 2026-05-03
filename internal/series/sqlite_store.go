package series

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/brunoga/pipeliner/internal/quality"
)

const schema = `
CREATE TABLE IF NOT EXISTS series_episodes (
	series_name   TEXT NOT NULL,
	episode_id    TEXT NOT NULL,
	downloaded_at INTEGER NOT NULL,
	quality       TEXT NOT NULL DEFAULT '{}',
	PRIMARY KEY (series_name, episode_id)
);
CREATE INDEX IF NOT EXISTS series_episodes_series ON series_episodes (series_name, downloaded_at DESC);
`

// SQLiteStore is a series tracker backed by a dedicated SQLite table.
// It supports efficient Latest() queries via an indexed column.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore initialises the series_episodes table in db and returns a SQLiteStore.
func NewSQLiteStore(db *sql.DB) (*SQLiteStore, error) {
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("series: create schema: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// IsSeen returns true if the episode has already been recorded.
func (s *SQLiteStore) IsSeen(seriesName, episodeID string) bool {
	var exists int
	err := s.db.QueryRow(
		`SELECT 1 FROM series_episodes WHERE series_name = ? AND episode_id = ? LIMIT 1`,
		seriesName, episodeID,
	).Scan(&exists)
	return err == nil
}

// Mark records that an episode has been downloaded.
func (s *SQLiteStore) Mark(r Record) error {
	if r.DownloadedAt.IsZero() {
		r.DownloadedAt = time.Now()
	}
	qualJSON, err := json.Marshal(r.Quality)
	if err != nil {
		return fmt.Errorf("series: mark: marshal quality: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO series_episodes (series_name, episode_id, downloaded_at, quality)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(series_name, episode_id) DO UPDATE SET
		   downloaded_at = excluded.downloaded_at,
		   quality = excluded.quality`,
		r.SeriesName, r.EpisodeID, r.DownloadedAt.UnixNano(), string(qualJSON),
	)
	if err != nil {
		return fmt.Errorf("series: mark %q %q: %w", r.SeriesName, r.EpisodeID, err)
	}
	return nil
}

// Latest returns the most recently downloaded episode for the given series.
func (s *SQLiteStore) Latest(seriesName string) (*Record, bool) {
	var episodeID string
	var downloadedAtNano int64
	var qualJSON string

	err := s.db.QueryRow(
		`SELECT episode_id, downloaded_at, quality
		 FROM series_episodes
		 WHERE series_name = ?
		 ORDER BY downloaded_at DESC
		 LIMIT 1`,
		seriesName,
	).Scan(&episodeID, &downloadedAtNano, &qualJSON)
	if err == sql.ErrNoRows {
		return nil, false
	}
	if err != nil {
		return nil, false
	}

	var q quality.Quality
	json.Unmarshal([]byte(qualJSON), &q) //nolint:errcheck // best-effort decode

	return &Record{
		SeriesName:   seriesName,
		EpisodeID:    episodeID,
		DownloadedAt: time.Unix(0, downloadedAtNano),
		Quality:      q,
	}, true
}
