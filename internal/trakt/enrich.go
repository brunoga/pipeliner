package trakt

import (
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/locale"
)

// ToVideoInfo maps an Item's extended=full fields onto entry.VideoInfo. Used
// by both metainfo_trakt (processor) and trakt_list (source) so the two emit
// identical fields from the same Trakt response.
//
// Series-only fields (Network, Status, FirstAired) and movie-only fields
// (Tagline) live on SeriesInfo/MovieInfo, so callers wrap the returned
// VideoInfo accordingly. Enriched is set to true.
func ToVideoInfo(it Item) entry.VideoInfo {
	vi := entry.VideoInfo{
		GenericInfo: entry.GenericInfo{
			Title:       it.Title,
			Description: it.Overview,
			Enriched:    true,
		},
		Year:          it.Year,
		Rating:        it.Rating,
		Votes:         it.Votes,
		Genres:        it.Genres,
		ImdbID:        it.IDs.IMDB,
		Runtime:       it.Runtime,
		Homepage:      it.Homepage,
		Language:      locale.LanguageName639_1(it.Language),
		Country:       locale.CountryName3166_1Alpha2(it.Country),
		ContentRating: it.Certification,
	}
	if it.Trailer != "" {
		vi.Trailers = []string{it.Trailer}
	}
	return vi
}

// FirstAiredDate truncates Item.FirstAired's leading 10 characters and parses
// them as a YYYY-MM-DD date. Returns the zero time when FirstAired is empty
// or unparseable. Trakt returns "2008-01-20T05:00:00.000Z"-style strings —
// only the date portion is meaningful for SeriesInfo.FirstAirDate.
func (it Item) FirstAiredDate() time.Time {
	s := it.FirstAired
	if len(s) >= 10 {
		s = s[:10]
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}
	}
	return t
}
