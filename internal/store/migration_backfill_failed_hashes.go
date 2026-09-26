package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/brunoga/pipeliner/internal/magnet"
)

func init() {
	migrations = append(migrations, migration{
		version:     4,
		description: "backfill info hashes for URL-keyed failed-grab records",
		fn:          migrateBackfillFailedHashes,
	})
}

// torrentCacheBucket is the bucket the metainfo_torrent processor caches
// decoded .torrent metadata in, keyed by the same release URL that
// seen_failed uses. Duplicated here rather than imported: internal/store
// cannot depend on a plugin package.
const torrentCacheBucket = "cache_metainfo_torrent"

// migrateBackfillFailedHashes gives pre-v1.35.2 failed-grab records the info
// hash they were stored without.
//
// Until v1.35.2 MarkFailed keyed seen_failed solely by release URL. Jackett
// re-encrypts its download links on every search, so a dead release came back
// under a fresh URL each run, missed the blocklist, and was grabbed again —
// on the live database 28 URL-keyed records turned out to be just 6 distinct
// releases, each re-downloaded and purged up to seven times. Recording the
// hash now would still leave those six free to come back one more time
// before their next failure is hash-keyed.
//
// For every URL-keyed record the hash is recovered from, in order:
//
//  1. the record's own info_hash field (already written by a newer binary),
//  2. the xt=urn:btih: parameter, when the key is a magnet URI,
//  3. the metainfo_torrent cache entry for the same URL.
//
// When a hash is found the URL-keyed record is stamped with it and an
// additional hash-keyed record is written, exactly as MarkFailed does today.
// The URL-keyed record is kept: it still blocks the precise link that failed,
// and entries that reach the seen filter before their hash is known can only
// be matched by URL. Records whose hash cannot be recovered (the cache entry
// expired) are left untouched — they simply keep their old URL-only reach.
func migrateBackfillFailedHashes(tx *sql.Tx) error {
	rows, err := tx.Query(
		`SELECT key, value FROM store WHERE bucket = ?`, FailedBucketName,
	)
	if err != nil {
		return fmt.Errorf("query failed records: %w", err)
	}
	type kv struct{ key, value string }
	var records []kv
	for rows.Next() {
		var r kv
		if err := rows.Scan(&r.key, &r.value); err != nil {
			rows.Close()
			return fmt.Errorf("scan failed record: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close rows: %w", err)
	}

	// Hash-keyed records written by this migration, so that several URL
	// records for the same release resolve last-write-wins by FailedAt
	// without re-reading the row each time.
	written := map[string]FailedRecord{}

	for _, r := range records {
		if isInfoHash(r.key) {
			continue // already hash-keyed
		}
		var rec FailedRecord
		if err := json.Unmarshal([]byte(r.value), &rec); err != nil {
			continue // unparseable record: leave it alone
		}

		hash := recoverInfoHash(tx, r.key, rec.InfoHash)
		if hash == "" {
			continue
		}

		// Stamp the URL-keyed record so it matches what MarkFailed writes.
		if rec.InfoHash != hash {
			rec.InfoHash = hash
			stamped, err := json.Marshal(rec)
			if err != nil {
				return fmt.Errorf("marshal record for %q: %w", r.key, err)
			}
			if _, err := tx.Exec(
				`UPDATE store SET value = ? WHERE bucket = ? AND key = ?`,
				string(stamped), FailedBucketName, r.key,
			); err != nil {
				return fmt.Errorf("stamp hash on %q: %w", r.key, err)
			}
		}

		// Last failure wins, matching MarkFailed overwriting on every call.
		if prev, ok := written[hash]; ok && !rec.FailedAt.After(prev.FailedAt) {
			continue
		}
		value, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("marshal hash record for %q: %w", hash, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO store (bucket, key, value) VALUES (?, ?, ?)
			 ON CONFLICT (bucket, key) DO UPDATE SET value = excluded.value`,
			FailedBucketName, hash, string(value),
		); err != nil {
			return fmt.Errorf("write hash record %q: %w", hash, err)
		}
		written[hash] = rec
	}
	return nil
}

// recoverInfoHash finds the info hash for a failed release URL: the hash
// already on the record, then the magnet URI itself, then the torrent
// metadata cache. Returns "" when none of them knows it.
func recoverInfoHash(tx *sql.Tx, url, recorded string) string {
	if h := strings.ToLower(recorded); isInfoHash(h) {
		return h
	}
	if strings.HasPrefix(url, "magnet:") {
		if m, err := magnet.Parse(url); err == nil {
			return m.InfoHash
		}
	}
	var cached string
	if err := tx.QueryRow(
		`SELECT value FROM store WHERE bucket = ? AND key = ?`,
		torrentCacheBucket, url,
	).Scan(&cached); err != nil {
		return ""
	}
	// cache.entry wraps the payload as {"v": …, "e": …}; bencode.TorrentInfo
	// has no JSON tags, so the field is spelled exactly "InfoHash".
	var envelope struct {
		Value struct {
			InfoHash string
		} `json:"v"`
	}
	if err := json.Unmarshal([]byte(cached), &envelope); err != nil {
		return ""
	}
	h := strings.ToLower(envelope.Value.InfoHash)
	if !isInfoHash(h) {
		return ""
	}
	return h
}

// isInfoHash reports whether s is a lowercase 40-character hex SHA-1, the
// form every info hash is stored in.
func isInfoHash(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
