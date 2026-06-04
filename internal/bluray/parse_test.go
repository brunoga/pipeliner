package bluray

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

func TestFormatFromSlug(t *testing.T) {
	cases := []struct {
		slug string
		want Format
	}{
		{"Avatar-3D-Blu-ray", FormatBD3D},
		{"Avatar-4K-Blu-ray", FormatUHD},
		{"Avatar-Blu-ray", FormatBD},
		{"Some-Movie-DVD", FormatDVD},
		{"avatar-3d-blu-ray", FormatBD3D}, // case-insensitive
		{"", FormatBD},                    // unknown defaults to BD
	}
	for _, tc := range cases {
		if got := FormatFromSlug(tc.slug); got != tc.want {
			t.Errorf("FormatFromSlug(%q) = %q, want %q", tc.slug, got, tc.want)
		}
	}
}

func TestSlugAndIDFromURL(t *testing.T) {
	cases := []struct {
		url, slug, id string
	}{
		{"https://www.blu-ray.com/movies/Avatar-3D-Blu-ray/26954/", "Avatar-3D-Blu-ray", "26954"},
		{"https://www.blu-ray.com/movies/Avatar-Blu-ray/7847/", "Avatar-Blu-ray", "7847"},
		{"https://example.com/", "", ""},
	}
	for _, tc := range cases {
		slug, id := SlugAndIDFromURL(tc.url)
		if slug != tc.slug || id != tc.id {
			t.Errorf("SlugAndIDFromURL(%q) = (%q, %q), want (%q, %q)", tc.url, slug, id, tc.slug, tc.id)
		}
	}
}

func TestParseDetail_Avatar3D(t *testing.T) {
	body := readFixture(t, "release_detail_avatar3d.html")
	r, err := ParseDetail(body, "https://www.blu-ray.com/movies/Avatar-3D-Blu-ray/26954/")
	if err != nil {
		t.Fatalf("ParseDetail: %v", err)
	}

	checks := []struct {
		field, got, want string
	}{
		{"ID", r.ID, "26954"},
		{"Format", string(r.Format), string(FormatBD3D)},
		{"Studio", r.Studio, "20th Century Fox"},
		{"Country", r.Country, "United States"},
		{"ReleaseDate", r.ReleaseDate, "2012-10-16"},
		{"Codec", r.Codec, "MPEG-4 MVC"},
		{"Resolution", r.Resolution, "1080p"},
		{"AspectRatio", r.AspectRatio, "1.78:1"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.field, c.got, c.want)
		}
	}
	if r.Year != 2009 {
		t.Errorf("Year: got %d, want 2009", r.Year)
	}
	if r.RuntimeMin != 162 {
		t.Errorf("RuntimeMin: got %d, want 162", r.RuntimeMin)
	}
	if !strings.HasPrefix(r.Title, "Avatar") {
		t.Errorf("Title: got %q, want prefix Avatar", r.Title)
	}
	if r.Edition == "" {
		t.Errorf("Edition: want non-empty (page title has parenthesised edition)")
	}
}

func TestParseDetail_EmptyBody(t *testing.T) {
	if _, err := ParseDetail([]byte("<html><body>nothing here</body></html>"),
		"https://www.blu-ray.com/movies/Foo-Blu-ray/0/"); err == nil {
		t.Fatalf("ParseDetail on empty page: want error, got nil")
	}
}

func TestParseSearch_Avatar(t *testing.T) {
	body := readFixture(t, "search_avatar.html")
	results, err := ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if len(results) < 5 {
		t.Fatalf("ParseSearch: got %d results, want at least 5", len(results))
	}

	// Specifically require the 3D Avatar release to be detected.
	var has3D, hasUHD, hasBD bool
	for _, r := range results {
		switch r.Format {
		case FormatBD3D:
			has3D = true
		case FormatUHD:
			hasUHD = true
		case FormatBD:
			hasBD = true
		}
		if r.ID == "" || r.Slug == "" {
			t.Errorf("empty id/slug: %+v", r)
		}
	}
	if !has3D {
		t.Error("expected at least one BD3D result for Avatar search")
	}
	if !hasUHD {
		t.Error("expected at least one UHD result for Avatar search")
	}
	if !hasBD {
		t.Error("expected at least one BD result for Avatar search")
	}

	// And the original 2009 Avatar 3D release is in there.
	var found bool
	for _, r := range results {
		if r.ID == "26954" {
			found = true
			if r.Format != FormatBD3D {
				t.Errorf("ID 26954 format: got %q, want BD3D", r.Format)
			}
			if r.Year != 2009 {
				t.Errorf("ID 26954 year: got %d, want 2009", r.Year)
			}
		}
	}
	if !found {
		t.Error("expected to find ID 26954 (Avatar 3D 2009) in search results")
	}
}

func TestParseSearch_YearFilteringHappensInClient(t *testing.T) {
	// ParseSearch itself does not filter by year — it returns every result.
	// The Client.SearchTitle wrapper applies year filtering.
	body := readFixture(t, "search_avatar.html")
	results, _ := ParseSearch(body)
	years := make(map[int]bool)
	for _, r := range results {
		years[r.Year] = true
	}
	if len(years) < 2 {
		t.Errorf("expected multiple distinct years in Avatar results, got %v", years)
	}
}

func TestParseCalendar_December2025(t *testing.T) {
	body := readFixture(t, "calendar_2025_12.html")
	rows, err := ParseCalendar(body)
	if err != nil {
		t.Fatalf("ParseCalendar: %v", err)
	}
	if len(rows) < 20 {
		t.Fatalf("ParseCalendar: got %d rows, want at least 20", len(rows))
	}

	// First row in the fixture: Hundreds of Beavers, id 402675, December 01, 2025.
	first := rows[0]
	if first.ID != "402675" {
		t.Errorf("first row ID: got %q, want 402675", first.ID)
	}
	if first.Title != "Hundreds of Beavers" {
		t.Errorf("first row Title: got %q, want Hundreds of Beavers", first.Title)
	}
	if first.Studio != "Cartuna" {
		t.Errorf("first row Studio: got %q, want Cartuna", first.Studio)
	}
	if first.Year != 2022 {
		t.Errorf("first row Year: got %d, want 2022", first.Year)
	}
	if first.ReleaseDate != "2025-12-01" {
		t.Errorf("first row ReleaseDate: got %q, want 2025-12-01", first.ReleaseDate)
	}
	if first.Format != FormatBD {
		t.Errorf("first row Format: got %q, want BD", first.Format)
	}

	// SteelBook casing should round-trip on at least some row.
	var sawSteelBook bool
	for _, r := range rows {
		if r.Casing == "SteelBook" {
			sawSteelBook = true
			break
		}
	}
	if !sawSteelBook {
		t.Error("expected to see at least one SteelBook casing in December 2025 calendar")
	}

	// 4K detection in title should map to UHD.
	var saw4K bool
	for _, r := range rows {
		if strings.HasSuffix(strings.ToLower(r.Title), " 4k") {
			saw4K = true
			if r.Format != FormatUHD {
				t.Errorf("title %q: got Format %q, want UHD", r.Title, r.Format)
			}
		}
	}
	if !saw4K {
		t.Error("expected to see at least one 4K release in December 2025")
	}
}

func TestIs3DRelease(t *testing.T) {
	all2D := []IndexEntry{
		{ID: "1", Format: FormatBD},
		{ID: "2", Format: FormatUHD},
	}
	if Is3DRelease(all2D) {
		t.Error("Is3DRelease: all-2D set returned true")
	}
	with3D := append(all2D, IndexEntry{ID: "3", Format: FormatBD3D})
	if !Is3DRelease(with3D) {
		t.Error("Is3DRelease: set including BD3D returned false")
	}
}
