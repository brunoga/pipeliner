// Package upgrade provides a filter that tracks the best quality already
// downloaded for each title and accepts a new entry only when it offers a
// better quality, up to a configurable ceiling.
//
// Requires metainfo/quality to have run so that the "quality" field is set.
// Series entries also need metainfo/series for the series_name + series_episode
// fields (used as the stable key instead of the raw title).
//
// Config keys:
//
//	target   - quality ceiling spec, e.g. "2160p bluray" (required)
//	on_lower - action when incoming quality ≤ stored: "reject" (default) or "accept"
package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "upgrade",
		Description: "accept entries only when they offer a quality improvement over what was previously downloaded",
		PluginPhase: plugin.PhaseFilter,
		Factory:     newPlugin,
	})
}

type upgradePlugin struct {
	target  quality.Spec
	onLower string // "reject" or "accept"
	db      *store.SQLiteStore
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	targetStr, _ := cfg["target"].(string)
	if targetStr == "" {
		return nil, fmt.Errorf("upgrade: 'target' quality spec is required (e.g. \"2160p\")")
	}
	target, err := quality.ParseSpec(targetStr)
	if err != nil {
		return nil, fmt.Errorf("upgrade: invalid target spec %q: %w", targetStr, err)
	}

	onLower, _ := cfg["on_lower"].(string)
	if onLower == "" {
		onLower = "reject"
	}
	if onLower != "reject" && onLower != "accept" {
		return nil, fmt.Errorf("upgrade: 'on_lower' must be \"reject\" or \"accept\"")
	}

	return &upgradePlugin{target: target, onLower: onLower, db: db}, nil
}

func (p *upgradePlugin) Name() string        { return "upgrade" }
func (p *upgradePlugin) Phase() plugin.Phase { return plugin.PhaseFilter }

func (p *upgradePlugin) Filter(_ context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	current := quality.Parse(e.Title)

	bucket := p.db.Bucket("upgrade:" + tc.Name)
	key := entryKey(e)

	var stored upgradeRecord
	found, err := bucket.Get(key, &stored)
	if err != nil {
		tc.Logger.Warn("upgrade: store lookup failed", "key", key, "err", err)
	}

	if !found {
		// First time seeing this title — always accept.
		e.Accept()
		return nil
	}

	// Already at or past target quality — stop upgrading.
	if stored.Quality != "" {
		storedQ := quality.Parse(stored.Quality)
		if p.target.Matches(storedQ) {
			e.Reject(fmt.Sprintf("upgrade: already at target quality %q for %q", stored.Quality, key))
			return nil
		}

		// Accept if current is strictly better.
		if current.Better(storedQ) {
			e.Accept()
			return nil
		}

		// Current is not better.
		if p.onLower == "reject" {
			e.Reject(fmt.Sprintf("upgrade: quality %q is not better than stored %q for %q",
				e.GetString("quality"), stored.Quality, key))
		} else {
			e.Accept()
		}
		return nil
	}

	// Stored record exists but has no quality string — treat as first download.
	e.Accept()
	return nil
}

func (p *upgradePlugin) Learn(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	bucket := p.db.Bucket("upgrade:" + tc.Name)
	for _, e := range entries {
		if !e.IsAccepted() {
			continue
		}
		key := entryKey(e)
		q := quality.Parse(e.Title)
		rec := upgradeRecord{
			Key:         key,
			Quality:     e.GetString("quality"),
			QualityStr:  q.String(),
			LastUpdated: time.Now().UTC(),
		}
		if rec.Quality == "" {
			rec.Quality = q.String()
		}
		_ = bucket.Put(key, rec)
	}
	return nil
}

// entryKey returns a stable identifier for a title. For series entries it
// combines series_name + episode ID; for everything else it uses the title.
func entryKey(e *entry.Entry) string {
	name := e.GetString("series_name")
	ep := e.GetString("series_id") // set by metainfo/series as "S01E01"
	if name != "" && ep != "" {
		return name + ":" + ep
	}
	return e.Title
}

type upgradeRecord struct {
	Key         string    `json:"key"`
	Quality     string    `json:"quality"`      // from entry field
	QualityStr  string    `json:"quality_str"`  // from quality.Parse
	LastUpdated time.Time `json:"last_updated"`
}

var _ json.Marshaler = (*upgradeRecord)(nil)

func (r upgradeRecord) MarshalJSON() ([]byte, error) {
	type alias upgradeRecord
	return json.Marshal(alias(r))
}
