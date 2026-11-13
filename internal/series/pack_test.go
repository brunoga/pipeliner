package series

import "testing"

func TestParsePack(t *testing.T) {
	for _, tc := range []struct {
		title       string
		name        string
		first, last int
		complete    bool
		id          string
	}{
		{"Agatha.All.Along.S01.1080p.DSNP.WEB-DL.DDP5.1.H.264-NTb", "Agatha All Along", 1, 1, false, "S01"},
		{"Agatha All Along S01 COMPLETE 1080p WEB", "Agatha All Along", 1, 1, false, "S01"},
		{"Breaking Bad Season 2 720p BluRay", "Breaking Bad", 2, 2, false, "S02"},
		{"Breaking.Bad.Season.02.1080p", "Breaking Bad", 2, 2, false, "S02"},
		{"The Wire S01-S05 1080p BluRay x265", "The Wire", 1, 5, false, "S01-S05"},
		{"The.Wire.S01-05.720p", "The Wire", 1, 5, false, "S01-S05"},
		{"Dark Seasons 1-3 1080p NF WEB", "Dark", 1, 3, false, "S01-S03"},
		{"Firefly The Complete Series 1080p BluRay", "Firefly", 0, 0, true, "COMPLETE"},
		{"Firefly.Complete.Series.720p", "Firefly", 0, 0, true, "COMPLETE"},
		{"Brothers 2026 S01 1080p WEB", "Brothers 2026", 1, 1, false, "S01"},
		{"Show S1 720p", "Show", 1, 1, false, "S01"},
	} {
		p, ok := ParsePack(tc.title)
		if !ok {
			t.Errorf("%q: not read as a pack", tc.title)
			continue
		}
		if p.SeriesName != tc.name || p.Season != tc.first || p.LastSeason != tc.last || p.Complete != tc.complete || PackID(p) != tc.id {
			t.Errorf("%q: got name %q seasons %d-%d complete %v id %q", tc.title, p.SeriesName, p.Season, p.LastSeason, p.Complete, PackID(p))
		}
	}
}

// TestParsePackRefuses: an episode is never a pack, and neither is a film or
// a title whose only "season" is a word.
func TestParsePackRefuses(t *testing.T) {
	for _, title := range []string{
		"Agatha.All.Along.S01E01.1080p.WEB",
		"Show 1x05 720p",
		"The Complete Metropolis 2010 1080p BluRay",
		"Season of the Witch 2011 1080p",
		"S1m0ne 2002 720p",
		"Show S00 720p",
		"Show S99 720p",
		"Show S03-S01 720p",
		"Some.Movie.2019.1080p.WEB",
		"",
	} {
		if p, ok := ParsePack(title); ok {
			t.Errorf("%q read as a pack: %+v", title, p)
		}
	}
}

func TestPackCoversAndMultiSeason(t *testing.T) {
	one, _ := ParsePack("Show S02 720p")
	if !one.Covers(2) || one.Covers(1) || one.Covers(3) || one.MultiSeason() {
		t.Errorf("single season: %+v", one)
	}
	run, _ := ParsePack("Show S01-S03 720p")
	if !run.Covers(1) || !run.Covers(3) || run.Covers(4) || !run.MultiSeason() {
		t.Errorf("range: %+v", run)
	}
	all, _ := ParsePack("Show Complete Series 720p")
	if !all.Covers(1) || !all.Covers(9) || all.Covers(0) || !all.MultiSeason() {
		t.Errorf("complete: %+v", all)
	}
}
