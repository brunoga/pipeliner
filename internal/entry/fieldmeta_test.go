package entry

import (
	"slices"
	"testing"
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
