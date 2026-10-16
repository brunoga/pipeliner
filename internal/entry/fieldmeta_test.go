package entry

import (
	"slices"
	"testing"

	"github.com/brunoga/pipeliner/quality"
)

func TestSeriesStatusFieldMeta(t *testing.T) {
	meta, ok := LookupField(FieldSeriesStatus)
	if !ok {
		t.Fatalf("%s not registered in KnownFields", FieldSeriesStatus)
	}
	if meta.Type != FieldTypeString {
		t.Errorf("type: got %v, want string", meta.Type)
	}
	// The list-management pipelines filter on all four TheTVDB values.
	for _, v := range []string{"Continuing", "Ended", "Cancelled", "Upcoming"} {
		if !slices.Contains(meta.KnownValues, v) {
			t.Errorf("KnownValues missing %q: %v", v, meta.KnownValues)
		}
	}
	for _, p := range []string{"tvdb_favorites", "trakt_list", "metainfo_tvdb", "metainfo_trakt"} {
		if !slices.Contains(meta.SetBy, p) {
			t.Errorf("SetBy missing %q: %v", p, meta.SetBy)
		}
	}
}

// TestLayoutKnownValuesCoverEveryFormat: video_3d_layout is written from
// quality.Format3D.Layout, so a new 3D format that is not listed here would
// be a value the editor never offers. It is checked against the parser's own
// list rather than a copy of it.
func TestLayoutKnownValuesCoverEveryFormat(t *testing.T) {
	meta, ok := LookupField(FieldVideoLayout3D)
	if !ok {
		t.Fatalf("%s not registered", FieldVideoLayout3D)
	}
	// Stored values are small and dense; well past the last one, Layout is "".
	for f := quality.Format3D(0); f < 64; f++ {
		l := f.Layout()
		if l == "" {
			continue
		}
		if !slices.Contains(meta.KnownValues, l) {
			t.Errorf("layout %q (Format3D %d) is missing from %s's KnownValues %v", l, int(f), FieldVideoLayout3D, meta.KnownValues)
		}
	}
}
