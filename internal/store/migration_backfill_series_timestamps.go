package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

func init() {
	migrations = append(migrations, migration{
		version:     2,
		description: "backfill zero downloaded_at in series tracker with migration run time",
		fn:          migrateBackfillSeriesTimestamps,
	})
}

// migrateBackfillSeriesTimestamps fixes series tracker records whose
// downloaded_at is the Go zero time ("0001-01-01T00:00:00Z").
//
// These zero timestamps were written during a bug window where persist()
// produced records with a missing or unset DownloadedAt field. The zero value
// breaks Latest() (which picks the most recently downloaded episode by
// timestamp): any record with a real timestamp always beats a zero-timestamp
// record, so Latest() would return the first-ever episode instead of the
// most advanced one, causing strict-mode gap checking to reject valid episodes.
//
// Each zero-timestamp record is updated to the time this migration runs.
// Records that share the same (now non-zero) timestamp fall back to the
// EpisodeID tie-breaker in Latest(), which returns the lexicographically
// greatest episode — i.e. the most advanced one. This is the correct behavior.
func migrateBackfillSeriesTimestamps(tx *sql.Tx) error {
	rows, err := tx.Query(`
		SELECT key, value FROM store
		WHERE bucket = 'series'
		  AND instr(value, '"downloaded_at":"0001-01-01T00:00:00Z"') > 0
	`)
	if err != nil {
		return fmt.Errorf("query zero-timestamp records: %w", err)
	}

	type kv struct{ key, value string }
	var stale []kv
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return fmt.Errorf("scan: %w", err)
		}
		stale = append(stale, kv{k, v})
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close rows: %w", err)
	}

	now := time.Now().UTC()
	for _, item := range stale {
		var rec map[string]json.RawMessage
		if err := json.Unmarshal([]byte(item.value), &rec); err != nil {
			continue
		}
		nowJSON, err := json.Marshal(now)
		if err != nil {
			return fmt.Errorf("marshal timestamp: %w", err)
		}
		rec["downloaded_at"] = nowJSON
		newValue, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("marshal record %q: %w", item.key, err)
		}
		if _, err := tx.Exec(
			`UPDATE store SET value=? WHERE bucket='series' AND key=?`,
			string(newValue), item.key,
		); err != nil {
			return fmt.Errorf("update %q: %w", item.key, err)
		}
	}
	return nil
}
