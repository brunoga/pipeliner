// Package upgrade provides a filter that tracks the best quality already
// downloaded for each title and accepts a new entry only when it offers a
// better quality, up to a configurable ceiling.
//
// Requires metainfo/quality to have run so that the "quality" field is set.
// Series entries also need a metainfo plugin (e.g. metainfo/series, metainfo/tvdb)
// to set the title and series_episode_id fields used as the stable key.
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
		Role:        plugin.RoleProcessor,
		Requires:    plugin.RequireAll(entry.FieldVideoQuality),
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "target",   Type: plugin.FieldTypeString, Required: true, Hint: `Quality ceiling spec, e.g. "2160p bluray"`},
			{Key: "on_lower", Type: plugin.FieldTypeEnum,                   Enum: []string{"reject", "accept"}, Hint: "Action when quality ≤ stored (default reject)"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "target", "upgrade"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptEnum(cfg, "on_lower", "upgrade", "reject", "accept"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "upgrade", "target", "on_lower")...)
	return errs
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

func (p *upgradePlugin) filter(_ context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
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
				e.GetString(entry.FieldVideoQuality), stored.Quality, key))
		} else {
			e.Accept()
		}
		return nil
	}

	// Stored record exists but has no quality string — treat as first download.
	e.Accept()
	return nil
}

func (p *upgradePlugin) persist(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	bucket := p.db.Bucket("upgrade:" + tc.Name)
	for _, e := range entries {
		key := entryKey(e)
		q := quality.Parse(e.Title)
		rec := upgradeRecord{
			Key:         key,
			Quality:     e.GetString(entry.FieldVideoQuality),
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
// combines the canonical title + episode ID; for everything else it uses the raw title.
func entryKey(e *entry.Entry) string {
	ep := e.GetString(entry.FieldSeriesEpisodeID)
	if ep != "" {
		if name := e.GetString(entry.FieldTitle); name != "" {
			return name + ":" + ep
		}
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

func (p *upgradePlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		if err := p.filter(ctx, tc, e); err != nil {
			tc.Logger.Warn("upgrade filter error", "entry", e.Title, "err", err)
		}
	}
	return entry.PassThrough(entries), nil
}

// Commit implements plugin.CommitPlugin. It persists quality records for all
// entries that were accepted by Process and not subsequently failed by any
// downstream sink. This ensures we only record quality upgrades when the full
// pipeline (including download/output) succeeded.
func (p *upgradePlugin) Commit(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	return p.persist(ctx, tc, entries)
}
