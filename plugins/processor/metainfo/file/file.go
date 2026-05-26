// Package file provides a metainfo plugin that parses the entry title
// (filename), classifies the entry as series, movie, or other, and annotates
// all detectable metadata fields in one pass.
//
// Classification order:
//
//  1. Series — requires a strong episode pattern (SxxExx, dates, 1x01, etc.).
//     When matched, sets series_* fields and media_type="series".
//  2. Movie — requires a year or quality marker. When matched, sets
//     movie_title, video_year, and media_type="movie".
//  3. Always — video quality fields (video_quality, video_resolution,
//     video_source, video_is_3d, codec, audio, color_range) are set when
//     any quality dimension is detected, regardless of classification.
//
// Series is tried first because a title like "Show.2023.S01E01.720p" also
// matches the (looser) movie parser. Series-first ensures correct classification.
//
// This plugin is strictly additive metadata: it never accepts or rejects
// entries. Use route() downstream to dispatch by media_type, or combine with
// the series/movies filters which match by their own criteria.
package file

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	imovies "github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "metainfo_file",
		Description: "parse entry filename, classify as series/movie, and annotate all detectable metadata in one pass",
		Role:        plugin.RoleProcessor,
		MayProduce: []string{
			entry.FieldTitle,
			entry.FieldMediaType,
			// Series fields (only when classified as series).
			entry.FieldSeriesSeason,
			entry.FieldSeriesEpisode,
			entry.FieldSeriesEpisodeID,
			entry.FieldSeriesDoubleEpisode,
			entry.FieldSeriesProper,
			entry.FieldSeriesRepack,
			entry.FieldSeriesService,
			"series_container",
			// Movie fields (only when classified as movie).
			entry.FieldMovieTitle,
			entry.FieldVideoYear,
			// Quality fields (set whenever any dimension is detected).
			entry.FieldVideoQuality,
			entry.FieldVideoResolution,
			entry.FieldVideoSource,
			entry.FieldVideoIs3D,
			"codec",
			"audio",
			"color_range",
			"quality_resolution",
			"quality_source",
		},
		Factory: newPlugin,
		Validate: func(cfg map[string]any) []error {
			return plugin.OptUnknownKeys(cfg, "metainfo_file")
		},
	})
}

// codecNames, audioNames, colorRangeNames mirror metainfo_quality so that the
// "codec", "audio", and "color_range" fields use the same string vocabulary
// regardless of which metainfo plugin set them.
var codecNames = map[quality.Codec]string{
	quality.CodecUnknown: "", quality.CodecXviD: "XviD", quality.CodecDivX: "DivX",
	quality.CodecH264: "H264", quality.CodecH265: "H265", quality.CodecAV1: "AV1",
}

var audioNames = map[quality.Audio]string{
	quality.AudioUnknown: "", quality.AudioMP3: "MP3", quality.AudioAAC: "AAC",
	quality.AudioDolbyDigital: "DD", quality.AudioDTS: "DTS",
	quality.AudioTrueHD: "TrueHD", quality.AudioAtmos: "Atmos",
}

var colorRangeNames = map[quality.ColorRange]string{
	quality.ColorRangeUnknown: "", quality.ColorRangeSDR: "SDR",
	quality.ColorRangeHDR: "HDR", quality.ColorRangeHDR10: "HDR10", quality.ColorRangeDolbyVision: "DV",
}

type filePlugin struct{}

func newPlugin(_ map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	return &filePlugin{}, nil
}

func (p *filePlugin) Name() string { return "metainfo_file" }

func (p *filePlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		annotate(e)
	}
	return entries, nil
}

// annotate runs the full classification + annotation pass on a single entry.
// The order matters: series is tried first because its parser is the strictest
// (requires an explicit episode marker); a title that matches as series may
// also parse as a movie (due to year detection) and series wins.
func annotate(e *entry.Entry) {
	if ep, ok := series.Parse(e.Title); ok {
		annotateSeries(e, ep)
		annotateQuality(e)
		return
	}
	if m, ok := imovies.Parse(e.Title); ok {
		annotateMovie(e, m)
		annotateQuality(e)
		return
	}
	annotateQuality(e)
}

func annotateSeries(e *entry.Entry, ep *series.Episode) {
	e.Set(entry.FieldMediaType, entry.MediaTypeSeries)
	if ep.Container != "" {
		e.Set("series_container", ep.Container)
	}
	e.SetSeriesInfo(entry.SeriesInfo{
		VideoInfo:     entry.VideoInfo{GenericInfo: entry.GenericInfo{Title: ep.SeriesName}},
		Season:        ep.Season,
		Episode:       ep.Episode,
		EpisodeID:     series.EpisodeID(ep),
		DoubleEpisode: ep.DoubleEpisode,
		Proper:        ep.Proper,
		Repack:        ep.Repack,
		Service:       ep.Service,
	})
}

func annotateMovie(e *entry.Entry, m *imovies.Movie) {
	e.Set(entry.FieldMediaType, entry.MediaTypeMovie)
	// SetMovieInfo writes the title to both FieldTitle (via GenericInfo) and
	// FieldMovieTitle (via the explicit check in SetMovieInfo), so a single
	// assignment in GenericInfo populates both.
	e.SetMovieInfo(entry.MovieInfo{
		VideoInfo: entry.VideoInfo{
			GenericInfo: entry.GenericInfo{Title: m.Title},
			Year:        m.Year,
		},
	})
}

// annotateQuality sets video_* and codec/audio/color_range fields when any
// quality dimension is detected. Mirrors the behaviour of metainfo_quality so
// existing pipelines see the same field shape.
func annotateQuality(e *entry.Entry) {
	q := quality.Parse(e.Title)
	if q == (quality.Quality{}) {
		return
	}
	e.SetVideoInfo(entry.VideoInfo{
		Quality:    q.String(),
		Resolution: q.ResolutionName(),
		Source:     q.SourceName(),
		Is3D:       q.Format3D != quality.Format3DNone,
	})
	setIfKnown(e, "codec", codecNames[q.Codec])
	setIfKnown(e, "audio", audioNames[q.Audio])
	setIfKnown(e, "color_range", colorRangeNames[q.ColorRange])
	e.Set("quality_resolution", fmt.Sprintf("%d", int(q.Resolution)))
	e.Set("quality_source", fmt.Sprintf("%d", int(q.Source)))
}

func setIfKnown(e *entry.Entry, key, val string) {
	if val != "" {
		e.Set(key, val)
	}
}
