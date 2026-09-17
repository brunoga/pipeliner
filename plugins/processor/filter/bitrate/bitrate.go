// Package bitrate gates torrent entries on their implied video bitrate —
// torrent size divided by runtime — the one quality signal a release name
// cannot lie about. A "2160p" encode at 12 Mbps is starved no matter what
// the name claims (the motivating case: a 4K 3D conversion that looked bad
// on screen but carried no CAM/TS marker). Both inputs are known before
// downloading: torrent_file_size comes from the indexer feed or
// metainfo_torrent, video_runtime from TMDb/TVDB enrichment.
//
// The computed rate is stamped on every entry as video_bitrate_mbps so
// condition rules can use it freely; the optional per-resolution floors
// reject below-threshold entries directly. Entries missing size or runtime
// are left untouched — the gate never rejects on absent data.
//
// Config keys (all optional, Mbps, 0 = no floor):
//
//	min_2160p - floor for 2160p entries
//	min_1080p - floor for 1080p entries
//	min_720p  - floor for 720p entries
//	min_other - floor for everything else (including unknown resolution)
package bitrate

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/store"
)

const pluginName = "bitrate"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "gate entries on implied bitrate (torrent size / runtime) — the quality signal a release name cannot fake",
		Role:        plugin.RoleProcessor,
		Requires: [][]string{
			{entry.FieldTorrentFileSize},
			{entry.FieldVideoRuntime},
		},
		MayProduce: []string{entry.FieldVideoBitrateMbps},
		Factory:    newPlugin,
		Validate:   validate,
		Schema: []plugin.FieldSchema{
			{Key: "min_2160p", Type: plugin.FieldTypeInt, Hint: "Minimum implied Mbps for 2160p entries (0 = no floor)"},
			{Key: "min_1080p", Type: plugin.FieldTypeInt, Hint: "Minimum implied Mbps for 1080p entries (0 = no floor)"},
			{Key: "min_720p", Type: plugin.FieldTypeInt, Hint: "Minimum implied Mbps for 720p entries (0 = no floor)"},
			{Key: "min_other", Type: plugin.FieldTypeInt, Hint: "Minimum implied Mbps for other/unknown resolutions (0 = no floor)"},
		},
	})
}

var floorKeys = []string{"min_2160p", "min_1080p", "min_720p", "min_other"}

func validate(cfg map[string]any) []error {
	var errs []error
	for _, k := range floorKeys {
		if v, ok := cfg[k]; ok {
			if n, isNum := toFloat(v); !isNum || n < 0 {
				errs = append(errs, fmt.Errorf("%s: %q must be a non-negative number of Mbps", pluginName, k))
			}
		}
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName, floorKeys...)...)
	return errs
}

type bitratePlugin struct {
	floors map[string]float64
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	floors := make(map[string]float64, len(floorKeys))
	for _, k := range floorKeys {
		if v, ok := cfg[k]; ok {
			n, isNum := toFloat(v)
			if !isNum || n < 0 {
				return nil, fmt.Errorf("%s: %q must be a non-negative number of Mbps", pluginName, k)
			}
			floors[k] = n
		}
	}
	return &bitratePlugin{floors: floors}, nil
}

func (p *bitratePlugin) Name() string { return pluginName }

func (p *bitratePlugin) Process(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		size := toInt64(e.Fields[entry.FieldTorrentFileSize])
		runtimeMin := e.GetInt(entry.FieldVideoRuntime)
		if size <= 0 || runtimeMin <= 0 {
			continue // never reject on absent data
		}
		mbps := float64(size) * 8 / (float64(runtimeMin) * 60) / 1e6
		// Round to one decimal so reasons and conditions read cleanly.
		mbps = float64(int(mbps*10+0.5)) / 10
		e.Set(entry.FieldVideoBitrateMbps, mbps)

		floorKey := "min_other"
		var resName string
		if q, ok := e.Quality(); ok {
			switch q.Resolution {
			case quality.Resolutionp2160:
				floorKey, resName = "min_2160p", "2160p"
			case quality.Resolutionp1080:
				floorKey, resName = "min_1080p", "1080p"
			case quality.Resolutionp720:
				floorKey, resName = "min_720p", "720p"
			}
		}
		if floor := p.floors[floorKey]; floor > 0 && mbps < floor {
			if resName == "" {
				resName = "unknown resolution"
			}
			e.Reject(fmt.Sprintf("%s: %.1f Mbps for %s below floor %.0f Mbps (starved encode)",
				pluginName, mbps, resName, floor))
			tc.Logger.Info(pluginName+": rejected starved encode",
				"entry", e.Title, "mbps", mbps, "resolution", resName, "floor", floor)
		}
	}
	return entry.PassThrough(entries), nil
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}
