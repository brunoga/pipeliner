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
