package movies

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		title     string
		wantTitle string
		wantYear  int
		wantOK    bool
	}{
		{"The.Matrix.1999.1080p.BluRay.x264", "The Matrix", 1999, true},
		{"Inception.2010.720p.HDTV", "Inception", 2010, true},
		{"2001.A.Space.Odyssey.1968.1080p.BluRay", "2001 A Space Odyssey", 1968, true},
		{"Avengers.Endgame.2019.2160p.UHD.BluRay.x265", "Avengers Endgame", 2019, true},
		{"The.Dark.Knight.2008.BluRay.1080p.x264-GROUP", "The Dark Knight", 2008, true},
		{"Parasite.2019.KOREAN.1080p.BluRay.x264", "Parasite", 2019, true},
		{"Some.Random.File.Without.Year.mkv", "", 0, false},
		{"No.Year.Here.720p.BluRay", "", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			m, ok := Parse(tt.title)
			if ok != tt.wantOK {
				t.Fatalf("Parse(%q) ok=%v, want %v", tt.title, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if m.Title != tt.wantTitle {
				t.Errorf("title: got %q, want %q", m.Title, tt.wantTitle)
			}
			if m.Year != tt.wantYear {
				t.Errorf("year: got %d, want %d", m.Year, tt.wantYear)
			}
		})
	}
}

func TestParse3D(t *testing.T) {
	cases := []struct {
		title  string
		want3D bool
	}{
		{"Avatar.2009.3D.1080p.BluRay.x264", true},
		{"Gravity.2013.HSBS.1080p.BluRay", true},
		{"Interstellar.2014.H-SBS.1080p", true},
		{"Pacific.Rim.2013.HALF-SBS.1080p.BluRay", true},
		{"Prometheus.2012.SBS.1080p.BluRay", true},
		{"Gravity.2013.HOU.1080p.BluRay", true},
		{"Avatar.2009.BD3D.1080p.BluRay", true},
		{"The.Dark.Knight.2008.1080p.BluRay.x264", false},
		{"Inception.2010.720p.HDTV", false},
	}
	for _, c := range cases {
		m, ok := Parse(c.title)
		if !ok {
			t.Errorf("Parse(%q): expected ok", c.title)
			continue
		}
		if m.Is3D != c.want3D {
			t.Errorf("Parse(%q).Is3D = %v, want %v", c.title, m.Is3D, c.want3D)
		}
	}
}

func TestParseQuality(t *testing.T) {
	m, ok := Parse("Inception.2010.1080p.BluRay.x264")
	if !ok {
		t.Fatal("expected ok")
	}
	if m.Quality.String() == "unknown" {
		t.Error("quality should be parsed from title")
	}
	if m.Quality.Resolution == 0 {
		t.Error("resolution should be parsed")
	}
}

func TestNormalizeTitle(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"The.Matrix.", "The Matrix"},
		{"2001.A.Space.Odyssey.", "2001 A Space Odyssey"},
		{"The_Dark_Knight.", "The Dark Knight"},
		{"Avengers - Endgame.", "Avengers Endgame"},
	}
	for _, tt := range tests {
		got := NormalizeTitle(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeTitle(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
