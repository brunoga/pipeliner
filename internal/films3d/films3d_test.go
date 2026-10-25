package films3d

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// listPage mirrors the layout of "List of 3D films (2005–present)": one table
// per year under a year heading, a row per film, the 3D release date in the
// second cell and notes in the last.
const listPage = `== Feature films ==
=== 2010s ===
====2012====
{| class="wikitable sortable" width="100%"
!Title
!Release date
!Country
!3D type
!Notes
|-
|''[[Star Wars: Episode I – The Phantom Menace]]''
|February 10, 2012
|United States
|Filmed in 2D
|3D conversion by [[Industrial Light & Magic]] (ILM) and Prime Focus. Originally released in 2D in 1999.
|-
|''[[James Cameron's Titanic]]''
|April 4, 2012
|United States
|Filmed in 2D
|3D conversion and design by Stereo D. Originally released in 2D in 1997.<ref>{{cite web|title=Titanic 3D|date=2011}}</ref>
|}
====2016====
{| class="wikitable sortable" width="100%"
|-
|''[[Rogue One: A Star Wars Story]]''
|December 16, 2016
|United States
|Filmed in 2D
|3D conversion by Stereo D.
|}
====2019====
{| class="wikitable sortable" width="100%"
|-
|''[[Frozen 2]]''
|November 22, 2019
|United States
|Computer animation
|
|}
====2020====
{| class="wikitable sortable" width="100%"
|-
|''[[Mulan (2020 film)|Mulan]]''
|September 11, 2020{{efn|The wide theatrical release was cancelled in the United States.}}
|United States
|Filmed in 2D
|3D conversion by [[DNEG]].
|}
====2025====
{| class="wikitable sortable" width="100%"
|-
|''[[Avatar: Fire and Ash]]''
|United States
|[[Fusion Camera System]]
|Also released in [[IMAX]] 3D.
|-
|[[Fast X]]<ref>{{Cite web |url=https://example.com |title=Fast X}}</ref>
|May 19, 2023
|United States
|Filmed in 2D
|
|-
|''[[Thunderbolts*]]''
| rowspan="2" |May 2, 2025
|United States
|Filmed in 2D
|
|-
|''[[The Fantastic Four: First Steps]]''
|July 25, 2025
|United States
|Filmed in 2D
|
|}
`

func TestParse(t *testing.T) {
	ix := NewIndex(Parse(listPage))
	cases := []struct {
		name   string
		titles []string
		year   int
		want   bool
	}{
		{"studio conversion matched at its 3D year", []string{"Mulan"}, 2020, true},
		{"remake of a listed title is not vouched for", []string{"Mulan"}, 1998, false},
		{"re-release matched at the original year", []string{"Titanic"}, 1997, true},
		{"possessive credit dropped", []string{"Titanic"}, 2012, true},
		{"re-release matched at the original year via the article", []string{"Star Wars: Episode I – The Phantom Menace"}, 1999, true},
		{"colon prefix", []string{"Rogue One"}, 2016, true},
		{"colon suffix", []string{"The Force Awakens"}, 2015, false},
		{"roman numeral reads as a number", []string{"Frozen II"}, 2019, true},
		{"row without a date takes the section year", []string{"Avatar Fire and Ash"}, 2025, true},
		{"unitalicised link with a reference", []string{"Fast X"}, 2023, true},
		{"year off by one tolerated", []string{"Rogue One A Star Wars Story"}, 2017, true},
		{"year off by two tolerated", []string{"Rogue One A Star Wars Story"}, 2014, true},
		{"year off by three refused", []string{"Rogue One A Star Wars Story"}, 2013, false},
		{"leading article ignored", []string{"The Mulan"}, 2020, true},
		{"longer title at the same year", []string{"Fast X Part One"}, 2023, true},
		{"longer title a year later refused", []string{"Fast X Part One"}, 2024, false},
		{"unlisted film", []string{"Fight Club"}, 1999, false},
		{"any of several titles", []string{"Star Wars Episode VIII", "Rogue One"}, 2016, true},
		{"punctuation in the listed title ignored", []string{"Thunderbolts"}, 2025, true},
		{"number word and digit agree", []string{"The Fantastic 4: First Steps"}, 2025, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, got := ix.Lookup(c.titles, c.year); got != c.want {
				t.Errorf("Lookup(%q, %d) = %v, want %v", c.titles, c.year, got, c.want)
			}
		})
	}
}

func TestParseNative(t *testing.T) {
	films := Parse(listPage)
	native := map[string]bool{}
	for _, f := range films {
		native[f.Title] = f.Native
	}
	if native["Mulan"] {
		t.Error("Mulan is a conversion")
	}
	if !native["Avatar: Fire and Ash"] {
		t.Error("Avatar: Fire and Ash was shot in 3D (Fusion Camera System)")
	}
}

func TestParseSkipsReferenceYears(t *testing.T) {
	// The Titanic row's reference is dated 2011; only 2012 (release) and
	// 1997 (original) are the film's years.
	for _, f := range Parse(listPage) {
		if f.Title != "James Cameron's Titanic" {
			continue
		}
		for _, y := range f.Years {
			if y == 2011 {
				t.Errorf("years %v include the reference's date", f.Years)
			}
		}
		return
	}
	t.Fatal("Titanic not parsed")
}

func TestKey(t *testing.T) {
	cases := map[string]string{
		"Frozen II":                  "frozen 2",
		"Frozen 2":                   "frozen 2",
		"Fast & Furious":             "fast and furious",
		"Star Wars: The Last Jedi":   "star wars the last jedi",
		"X":                          "x",
		"The Darkest Hour":           "darkest hour",
		"The":                        "the",
		"Tim Burton's The Nightmare": "tim burtons the nightmare",
		"Thunderbolts*":              "thunderbolts",
		"Who Framed Roger Rabbit?":   "who framed roger rabbit",
		"The Fantastic Four":         "fantastic 4",
		"The Fantastic 4":            "fantastic 4",
		"Rogue One":                  "rogue 1",
	}
	for in, want := range cases {
		if got := Key(in); got != want {
			t.Errorf("Key(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFetch(t *testing.T) {
	var gotTitles []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/w/index.php" || r.URL.Query().Get("action") != "raw" {
			http.NotFound(w, r)
			return
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "pipeliner") {
			t.Errorf("User-Agent %q does not identify the client", r.Header.Get("User-Agent"))
		}
		gotTitles = append(gotTitles, r.URL.Query().Get("title"))
		_, _ = w.Write([]byte(listPage))
	}))
	defer srv.Close()

	films, err := Fetch(context.Background(), srv.Client(), srv.URL, []string{"A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	if len(gotTitles) != 2 || gotTitles[0] != "A" || gotTitles[1] != "B" {
		t.Errorf("fetched %q", gotTitles)
	}
	if want := 2 * len(Parse(listPage)); len(films) != want {
		t.Errorf("got %d films, want %d", len(films), want)
	}
}

func TestFetchRejectsAnEmptyPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("#REDIRECT [[Somewhere else]]"))
	}))
	defer srv.Close()
	if _, err := Fetch(context.Background(), srv.Client(), srv.URL, []string{"A"}); err == nil {
		t.Fatal("a page with no films must be an error, not an empty list that rejects everything")
	}
}

func TestFetchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	if _, err := Fetch(context.Background(), srv.Client(), srv.URL, []string{"A"}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestMergeKeepsFilmsTheNewListDropped(t *testing.T) {
	prev := Parse(listPage)
	next := []Film{{Title: "Gravity", Keys: []string{"gravity"}, Years: []int{2013}}}
	merged := NewIndex(Merge(prev, next))
	for _, q := range []struct {
		title string
		year  int
	}{{"Gravity", 2013}, {"Mulan", 2020}, {"Titanic", 1997}} {
		if _, ok := merged.Lookup([]string{q.title}, q.year); !ok {
			t.Errorf("%s (%d) lost in the merge", q.title, q.year)
		}
	}
	if n := len(Merge(prev, prev)); n != len(prev) {
		t.Errorf("merging a list with itself gave %d films, want %d", n, len(prev))
	}
}

func TestIndexRenormalisesStoredKeys(t *testing.T) {
	// A list stored before "The" was dropped from keys.
	old := []Film{{Title: "The Darkest Hour", Keys: []string{"the darkest hour"}, Years: []int{2011}}}
	if _, ok := NewIndex(old).Lookup([]string{"Darkest Hour"}, 2011); !ok {
		t.Error("a key stored by an older build no longer matches")
	}
}
