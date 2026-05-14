// Package quality provides a metainfo plugin that annotates entries with video quality fields.
package quality

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	q "github.com/brunoga/pipeliner/internal/quality"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "metainfo_quality",
		Description: "parse video quality from entry title and annotate fields",
		Role:        plugin.RoleProcessor,
		Produces: []string{
			entry.FieldVideoQuality,
			entry.FieldVideoResolution,
			entry.FieldVideoSource,
			entry.FieldVideoIs3D,
			"quality_resolution",
			"quality_source",
		},
		MayProduce: []string{
			"codec",
			"audio",
			"color_range",
		},
		Factory: newPlugin,
		Validate: func(cfg map[string]any) []error {
			return plugin.OptUnknownKeys(cfg, "metainfo_quality")
		},
	})
}

var codecNames = map[q.Codec]string{
	q.CodecUnknown: "", q.CodecXviD: "XviD", q.CodecDivX: "DivX",
	q.CodecH264: "H264", q.CodecH265: "H265", q.CodecAV1: "AV1",
}

var audioNames = map[q.Audio]string{
	q.AudioUnknown: "", q.AudioMP3: "MP3", q.AudioAAC: "AAC",
	q.AudioDolbyDigital: "DD", q.AudioDTS: "DTS",
	q.AudioTrueHD: "TrueHD", q.AudioAtmos: "Atmos",
}

var colorRangeNames = map[q.ColorRange]string{
	q.ColorRangeUnknown: "", q.ColorRangeSDR: "SDR",
	q.ColorRangeHDR: "HDR", q.ColorRangeHDR10: "HDR10", q.ColorRangeDolbyVision: "DV",
}

type qualityMetaPlugin struct{}

func newPlugin(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	return &qualityMetaPlugin{}, nil
}

func (p *qualityMetaPlugin) Name() string        { return "metainfo_quality" }

func (p *qualityMetaPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		qual := q.Parse(e.Title)
		e.SetVideoInfo(entry.VideoInfo{
			Quality:    qual.String(),
			Resolution: qual.ResolutionName(),
			Source:     qual.SourceName(),
			Is3D:       qual.Format3D != q.Format3DNone,
		})
		setIfKnown(e, "codec", codecNames[qual.Codec])
		setIfKnown(e, "audio", audioNames[qual.Audio])
		setIfKnown(e, "color_range", colorRangeNames[qual.ColorRange])
		e.Set("quality_resolution", fmt.Sprintf("%d", int(qual.Resolution)))
		e.Set("quality_source", fmt.Sprintf("%d", int(qual.Source)))
	}
	return entries, nil
}

func setIfKnown(e *entry.Entry, key, val string) {
	if val != "" {
		e.Set(key, val)
	}
}
