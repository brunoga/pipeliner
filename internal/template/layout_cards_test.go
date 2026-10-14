package template

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	ttpl "text/template"
	"time"
)

// Renders the episode cards out of the sample layout with a genre list, to
// prove the row reaches the HTML rather than merely parsing.
func TestEpisodeCardsShowGenres(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "configs", "email-report-layout.star"))
	if err != nil {
		t.Skip("sample layout not readable")
	}
	s := string(src)
	for _, card := range []string{"EPISODE_CARD", "PREMIERE_CARD"} {
		i := strings.Index(s, card+` = """`)
		if i < 0 {
			t.Fatalf("%s not found in the sample layout", card)
		}
		start := i + len(card) + 6
		j := strings.Index(s[start:], `"""`)
		body := "{{$e := .}}" + s[start:start+j]

		tpl, err := ttpl.New(card).Funcs(FuncMap()).Parse(body)
		if err != nil {
			t.Fatalf("%s: parse: %v", card, err)
		}
		data := map[string]any{
			"Title": "Some.Show.S01E01.1080p.WEB-DL-GROUP",
			"Fields": map[string]any{
				"title":                   "Some Show",
				"series_episode_id":       "S01E01",
				"series_episode_title":    "Pilot",
				"series_network":          "Netflix",
				"video_language":          "English",
				"video_genres":            []string{"Science Fiction", "Drama"},
				"torrent_file_size":       int64(2_400_000_000),
				"series_episode_air_date": time.Now(),
			},
		}
		var out strings.Builder
		if err := tpl.Execute(&out, data); err != nil {
			t.Fatalf("%s: execute: %v", card, err)
		}
		html := out.String()
		if !strings.Contains(html, ">Genres</td>") {
			t.Errorf("%s: no Genres row in the rendered HTML", card)
		}
		if !strings.Contains(html, "Science Fiction, Drama") {
			t.Errorf("%s: the genre list should be joined with commas; got:\n%s",
				card, snippet(html, "Genres"))
		}
		// The row has to disappear when the show has no genres, since the
		// field is MayProduce.
		data["Fields"].(map[string]any)["video_genres"] = []string{}
		var bare strings.Builder
		if err := tpl.Execute(&bare, data); err != nil {
			t.Fatalf("%s: execute without genres: %v", card, err)
		}
		if strings.Contains(bare.String(), ">Genres</td>") {
			t.Errorf("%s: an empty genre list must not leave an empty row", card)
		}
	}
}

var reTag = regexp.MustCompile(`<[^>]+>`)

func snippet(html, around string) string {
	i := strings.Index(html, around)
	if i < 0 {
		return "(not found)"
	}
	lo, hi := i-40, i+200
	if lo < 0 {
		lo = 0
	}
	if hi > len(html) {
		hi = len(html)
	}
	return reTag.ReplaceAllString(html[lo:hi], " ")
}

// Without signed action links configured, signedaction returns "" and the
// premiere card drops its Follow button. It used to fall back to the text
// "Automatically added", which was never true — nothing in the sample adds a
// show — and a live config copied from the sample went on telling its reader
// a show had been followed when it had not.
func TestPremiereCardDoesNotClaimAutoAddWithoutLinks(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "configs", "email-report-layout.star"))
	if err != nil {
		t.Skip("sample layout not readable")
	}
	s := string(src)
	i := strings.Index(s, `PREMIERE_CARD = """`)
	if i < 0 {
		t.Fatal("PREMIERE_CARD not found in the sample layout")
	}
	start := i + len(`PREMIERE_CARD = """`)
	j := strings.Index(s[start:], `"""`)
	tpl, err := ttpl.New("p").Funcs(FuncMap()).Parse("{{$e := .}}" + s[start:start+j])
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var out strings.Builder
	if err := tpl.Execute(&out, map[string]any{
		"Title":  "Some.Show.S01E01.1080p.WEB-DL-GROUP",
		"Fields": map[string]any{"title": "Some Show", "tvdb_id": 123},
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	html := out.String()
	if strings.Contains(html, "Automatically added") {
		t.Error("the card claims the show was added automatically, but nothing added it")
	}
	if strings.Contains(html, "Add to favorites</a>") {
		t.Error("with no links configured there is no link to offer")
	}
}
