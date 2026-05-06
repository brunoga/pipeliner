package series

import (
	"testing"
)

func TestParseStandardPattern(t *testing.T) {
	cases := []struct {
		title  string
		season int
		ep     int
		name   string
	}{
		{"Show.Name.S03E07.720p.HDTV", 3, 7, "Show Name"},
		{"My.Show.S01E01.1080p.BluRay.x264-GROUP", 1, 1, "My Show"},
		{"Another.Show.S10E12.WEB-DL", 10, 12, "Another Show"},
		{"Show.S01E01.PROPER.720p.HDTV", 1, 1, "Show"},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			ep, ok := Parse(tc.title)
			if !ok {
				t.Fatalf("Parse(%q) = false, want true", tc.title)
			}
			if ep.Season != tc.season {
				t.Errorf("season: got %d, want %d", ep.Season, tc.season)
			}
			if ep.Episode != tc.ep {
				t.Errorf("episode: got %d, want %d", ep.Episode, tc.ep)
			}
			if ep.SeriesName != tc.name {
				t.Errorf("name: got %q, want %q", ep.SeriesName, tc.name)
			}
		})
	}
}

func TestParseDoubleEpisode(t *testing.T) {
	ep, ok := Parse("Show.S01E01E02.720p.HDTV")
	if !ok {
		t.Fatal("expected match")
	}
	if ep.Episode != 1 || ep.DoubleEpisode != 2 {
		t.Errorf("double episode: ep=%d double=%d", ep.Episode, ep.DoubleEpisode)
	}
}

func TestParseAltNumPattern(t *testing.T) {
	ep, ok := Parse("Show.Name.3x07.720p.HDTV")
	if !ok {
		t.Fatal("expected match")
	}
	if ep.Season != 3 || ep.Episode != 7 {
		t.Errorf("got S%dE%d, want S3E7", ep.Season, ep.Episode)
	}
	if ep.SeriesName != "Show Name" {
		t.Errorf("name: got %q, want %q", ep.SeriesName, "Show Name")
	}
}

func TestParseDatePattern(t *testing.T) {
	cases := []struct {
		title   string
		y, m, d int
		name    string
	}{
		{"Show.2023.11.15.HDTV", 2023, 11, 15, "Show"},
		{"Show.Name.2020-03-25.720p", 2020, 3, 25, "Show Name"},
		// Space-separated dates (talk shows, daily episodes)
		{"Seth Meyers 2026 05 05 Chris Hayes 720p WEB H264-JFF", 2026, 5, 5, "Seth Meyers"},
		{"The Kelly Clarkson Show 2026 05 04 Tara Lipinski 1080p WEB h264-DiRT", 2026, 5, 4, "The Kelly Clarkson Show"},
		{"Jimmy Fallon 2026 05 05 Justin Hartley 1080p HEVC x265-MeGusta", 2026, 5, 5, "Jimmy Fallon"},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			ep, ok := Parse(tc.title)
			if !ok {
				t.Fatalf("Parse(%q) = false", tc.title)
			}
			if !ep.IsDate {
				t.Error("expected IsDate=true")
			}
			if ep.Year != tc.y || ep.Month != tc.m || ep.Day != tc.d {
				t.Errorf("date: got %d-%d-%d, want %d-%d-%d",
					ep.Year, ep.Month, ep.Day, tc.y, tc.m, tc.d)
			}
			if tc.name != "" && ep.SeriesName != tc.name {
				t.Errorf("series name: got %q, want %q", ep.SeriesName, tc.name)
			}
		})
	}
}

func TestParseEpisodeWordPattern(t *testing.T) {
	cases := []struct {
		title string
		ep    int
		name  string
	}{
		{"UFC 328 Embedded Vlog Series Episode 2 1080p WEBRip", 2, "UFC 328 Embedded Vlog Series"},
		{"UFC 328 Embedded-Vlog Series-Episode 2 1080p WEBRip", 2, "UFC 328 Embedded Vlog Series"},
		{"Some Show Episode 10 720p HDTV", 10, "Some Show"},
		{"Some.Show.Episode.5.720p", 5, "Some Show"},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			ep, ok := Parse(tc.title)
			if !ok {
				t.Fatalf("Parse(%q) = false", tc.title)
			}
			if ep.Episode != tc.ep {
				t.Errorf("episode: got %d, want %d", ep.Episode, tc.ep)
			}
			if ep.SeriesName != tc.name {
				t.Errorf("series name: got %q, want %q", ep.SeriesName, tc.name)
			}
		})
	}
}

func TestParseOrdinalDatePattern(t *testing.T) {
	cases := []struct {
		title   string
		y, m, d int
		name    string
	}{
		{"Eastenders 6th May 2026 1080 Deep71", 2026, 5, 6, "Eastenders"},
		{"TNA Xplosion 1st May 2026 YT 720p WEBRip", 2026, 5, 1, "TNA Xplosion"},
		{"Some Show 23rd December 2025 720p HDTV", 2025, 12, 23, "Some Show"},
		{"Late Night 2nd January 2026 1080p WEB", 2026, 1, 2, "Late Night"},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			ep, ok := Parse(tc.title)
			if !ok {
				t.Fatalf("Parse(%q) = false", tc.title)
			}
			if !ep.IsDate {
				t.Error("expected IsDate=true")
			}
			if ep.Year != tc.y || ep.Month != tc.m || ep.Day != tc.d {
				t.Errorf("date: got %d-%d-%d, want %d-%d-%d",
					ep.Year, ep.Month, ep.Day, tc.y, tc.m, tc.d)
			}
			if ep.SeriesName != tc.name {
				t.Errorf("series name: got %q, want %q", ep.SeriesName, tc.name)
			}
		})
	}
}

func TestParseAbsolutePattern(t *testing.T) {
	ep, ok := Parse("Anime.Title.E123.720p")
	if !ok {
		t.Fatal("expected match")
	}
	if ep.Episode != 123 {
		t.Errorf("absolute episode: got %d, want 123", ep.Episode)
	}
}

func TestParseProper(t *testing.T) {
	cases := []struct{ title string; proper, repack bool }{
		{"Show.S01E01.PROPER.720p", true, false},
		{"Show.S01E01.REPACK.720p", false, true},
		{"Show.S01E01.RERIP.720p", false, true},
		{"Show.S01E01.720p", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			ep, ok := Parse(tc.title)
			if !ok {
				t.Fatalf("Parse(%q) = false", tc.title)
			}
			if ep.Proper != tc.proper {
				t.Errorf("Proper: got %v, want %v", ep.Proper, tc.proper)
			}
			if ep.Repack != tc.repack {
				t.Errorf("Repack: got %v, want %v", ep.Repack, tc.repack)
			}
		})
	}
}

func TestParseQualityExtracted(t *testing.T) {
	ep, ok := Parse("Show.S01E01.1080p.BluRay.x264")
	if !ok {
		t.Fatal("expected match")
	}
	if ep.Quality.Resolution == 0 {
		t.Error("expected quality to be parsed")
	}
}

func TestParseNoMatch(t *testing.T) {
	titles := []string{
		"Just.A.Movie.2023.1080p.BluRay",
		"Random.Text.Without.Episode",
		"",
	}
	for _, title := range titles {
		if _, ok := Parse(title); ok {
			t.Errorf("Parse(%q) = true, want false", title)
		}
	}
}

func TestParseService(t *testing.T) {
	cases := []struct {
		title   string
		service string
		name    string
	}{
		{"Show.Name.S01E01.NF.1080p", "Netflix", "Show Name"},
		{"Show.Name.S01E01.AMZN.WEB-DL", "AMZN", "Show Name"},
		{"Show.Name.S01E01.ATVP.WEB-DL", "ATVP", "Show Name"},
		{"Show.Name.S01E01.DSNP.1080p", "DSNP", "Show Name"},
		{"Show.Name.S01E01.HMAX.WEB-DL", "HMAX", "Show Name"},
		{"Show.Name.S01E01.HULU.WEB-DL", "HULU", "Show Name"},
		{"Show.Name.S01E01.720p.HDTV", "", "Show Name"}, // no service tag
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			ep, ok := Parse(tc.title)
			if !ok {
				t.Fatalf("Parse(%q) = false", tc.title)
			}
			if ep.Service != tc.service {
				t.Errorf("Service: got %q, want %q", ep.Service, tc.service)
			}
			if ep.SeriesName != tc.name {
				t.Errorf("SeriesName: got %q, want %q", ep.SeriesName, tc.name)
			}
		})
	}
}

func TestParseContainer(t *testing.T) {
	cases := []struct {
		title     string
		container string
	}{
		{"Show.S01E01.1080p.BluRay.mkv", "mkv"},
		{"Show.S01E01.720p.HDTV.mp4", "mp4"},
		{"Show.S01E01.HDTV.avi", "avi"},
		{"Show.S01E01.720p.HDTV", ""}, // no extension
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			ep, ok := Parse(tc.title)
			if !ok {
				t.Fatalf("Parse(%q) = false", tc.title)
			}
			if ep.Container != tc.container {
				t.Errorf("Container: got %q, want %q", ep.Container, tc.container)
			}
		})
	}
}

func TestNormalizeName(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"Show.Name.", "Show Name"},
		{"the.dark.knight", "The Dark Knight"},
		{"My_Show_Name", "My Show Name"},
		{"Show-Name-", "Show Name"},
		{"  spaced  ", "Spaced"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got := NormalizeName(tc.raw)
			if got != tc.want {
				t.Errorf("NormalizeName(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
