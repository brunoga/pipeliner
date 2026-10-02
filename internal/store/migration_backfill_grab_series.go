package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

func init() {
	migrations = append(migrations, migration{
		version:     6,
		description: "backfill series names on grab records that have none",
		fn:          migrateBackfillGrabSeries,
	})
}

// reMigrateEpisodeMarker finds where a release title stops naming the show and
// starts naming the episode: " S01E01", ".S1E1", "-s01e001". Frozen copy of
// the shape series.ParseEpisode accepts for the SxxEyy form; a migration must
// keep splitting titles the same way even as that parser grows.
var reMigrateEpisodeMarker = regexp.MustCompile(`(?i)[ ._-]s\d{1,2}e\d{1,3}`)

// migrateBackfillGrabSeries fills in the series tracker key on grab records
// that recorded an episode ID but no series name.
//
// A grab record is how mark_failed walks back from a dead torrent in the
// download client to the release that produced it, and its SeriesName /
// EpisodeID are what let it un-track the episode so another release is tried.
// The premiere filter did not stamp the tracker key onto its entries, so every
// episode it grabbed produced a record the janitor could not un-track: a
// premiere whose download died stayed counted as downloaded forever. The
// filter stamps it now, but the records already written still cannot be
// resolved, and those are exactly the downloads old enough to have stalled.
//
// The key is recovered from the title rather than re-derived from a parser:
// cut the title at the episode marker, normalize what precedes it, and find
// the show whose tracker records include this episode. Matching through the
// tracker — rather than composing a key — guarantees the name written is one
// Tracker.Forget can actually delete, which is the only thing it is for.
//
// A grab with no matching tracker record is left alone. There is nothing to
// un-track, and a composed key that matches no record would only make
// mark_failed delete nothing while looking like it had worked.
//
// This runs after the trailing-year merge (version 5) by construction: before
// it, a show recorded under both "brothers" and "brothers 2026" offers two
// candidate keys for the same episode and no way to choose.
func migrateBackfillGrabSeries(tx *sql.Tx) error {
	episodes, err := showEpisodeIndex(tx)
	if err != nil {
		return err
	}
	if len(episodes) == 0 {
		return nil
	}

	// base → show names, so a title that omits the premiere year still finds
	// the year-carrying key the records live under (and the reverse).
	byBase := map[string][]string{}
	for name := range episodes {
		base := name
		if m := reMigrateTrailingYear.FindStringSubmatch(name); m != nil {
			base = m[1]
		}
		byBase[base] = append(byBase[base], name)
	}

	rows, err := tx.Query(`
		SELECT key, value FROM store
		WHERE bucket = 'grabs'
		  AND json_extract(value, '$.episode_id') IS NOT NULL
		  AND COALESCE(json_extract(value, '$.series_name'), '') = ''
	`)
	if err != nil {
		return fmt.Errorf("query grabs: %w", err)
	}
	type kv struct{ key, value string }
	var pending []kv
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan grab: %w", err)
		}
		pending = append(pending, kv{k, v})
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close grabs: %w", err)
	}

	for _, g := range pending {
		var rec map[string]json.RawMessage
		if err := json.Unmarshal([]byte(g.value), &rec); err != nil {
			continue
		}
		var title, epID string
		_ = json.Unmarshal(rec["title"], &title)
		_ = json.Unmarshal(rec["episode_id"], &epID)
		if title == "" || epID == "" {
			continue
		}
		name, ok := showForTitle(title, epID, byBase, episodes)
		if !ok {
			continue
		}
		encoded, err := json.Marshal(name)
		if err != nil {
			return fmt.Errorf("marshal show name %q: %w", name, err)
		}
		rec["series_name"] = encoded
		out, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("marshal grab %q: %w", g.key, err)
		}
		if _, err := tx.Exec(
			`UPDATE store SET value=? WHERE bucket='grabs' AND key=?`, string(out), g.key,
		); err != nil {
			return fmt.Errorf("update grab %q: %w", g.key, err)
		}
	}
	return nil
}

// showForTitle names the tracked show a release title belongs to, provided
// exactly one candidate holds a record for episodeID.
func showForTitle(title, episodeID string, byBase map[string][]string, episodes map[string]map[string]bool) (string, bool) {
	m := reMigrateEpisodeMarker.FindStringIndex(title)
	if m == nil {
		return "", false
	}
	name := migrateNormalizeShow(title[:m[0]])
	if name == "" {
		return "", false
	}
	base := name
	if y := reMigrateTrailingYear.FindStringSubmatch(name); y != nil {
		base = y[1]
	}
	var found []string
	for _, cand := range byBase[base] {
		if episodes[cand][episodeID] {
			found = append(found, cand)
		}
	}
	if len(found) != 1 {
		return "", false // unknown show, or an ambiguity we must not guess at
	}
	return found[0], true
}

// showEpisodeIndex maps each tracked show name to the episode IDs it has
// records for.
func showEpisodeIndex(tx *sql.Tx) (map[string]map[string]bool, error) {
	rows, err := tx.Query(`SELECT key FROM store WHERE bucket='series' AND instr(key, '|') > 0`)
	if err != nil {
		return nil, fmt.Errorf("query series keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("scan series key: %w", err)
		}
		name, epID, ok := splitShowKey(k)
		if !ok {
			continue
		}
		if out[name] == nil {
			out[name] = map[string]bool{}
		}
		out[name][epID] = true
	}
	return out, rows.Err()
}

// migrateNormalizeShow is a frozen copy of match.Normalize: lowercase, treat
// '.', '_' and '-' as spaces, collapse runs of whitespace.
func migrateNormalizeShow(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if r == '.' || r == '_' || r == '-' {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}
