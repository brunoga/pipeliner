package entry

// Standard field name constants for entries. Provider-specific fields
// (tvdb_*, tmdb_*, trakt_*, etc.) are set in addition to these and retain
// their raw API values; the standard fields hold normalised, human-readable
// equivalents suitable for use in conditions, pathfmt patterns, and templates.
const (
	// Tier 1 — Generic (any entry type).
	FieldTitle       = "title"
	FieldDescription = "description"

	// Tier 2 — Video (shared by movies and series).
	FieldYear          = "year"
	FieldPublishedDate = "published_date"
	FieldLanguage      = "language"
	FieldOriginalTitle = "original_title"
	FieldCountry       = "country"
	FieldGenres        = "genres"
	FieldRating        = "rating"
	FieldPoster        = "poster"
	FieldCast          = "cast"
	FieldContentRating = "content_rating"
	FieldRuntime       = "runtime"
	FieldTrailers      = "trailers"
	FieldAliases       = "aliases"
	FieldImdbID        = "imdb_id"

	// Tier 3 — Series only.
	FieldSeason             = "season"
	FieldEpisode            = "episode"
	FieldEpisodeID          = "episode_id"
	FieldNetwork            = "network"
	FieldStatus             = "status"
	FieldFirstAirDate       = "first_air_date"
	FieldLastAirDate        = "last_air_date"
	FieldNextAirDate        = "next_air_date"
	FieldEpisodeTitle       = "episode_title"
	FieldEpisodeDescription = "episode_description"
	FieldEpisodeAirDate     = "episode_air_date"
	FieldEpisodeImage       = "episode_image"
)

// GenericInfo holds Tier-1 fields applicable to any entry type (RSS, torrents, etc.).
type GenericInfo struct {
	Title       string
	Description string
}

// VideoInfo holds Tier-2 fields shared by all video content (movies and series).
type VideoInfo struct {
	GenericInfo
	Year          int
	PublishedDate string
	Language      string
	OriginalTitle string
	Country       string
	Genres        []string
	Rating        float64
	Poster        string
	Cast          []string
	ContentRating string
	Runtime       int
	Trailers      []string
	Aliases       []string
	ImdbID        string
}

// MovieInfo holds movie-specific fields on top of VideoInfo.
// No movie-only fields exist yet; the type is defined for future extensibility
// and semantic clarity (callers use SetMovieInfo, not SetVideoInfo, for movies).
type MovieInfo struct {
	VideoInfo
}

// SeriesInfo holds series-specific fields on top of VideoInfo.
type SeriesInfo struct {
	VideoInfo
	Season              int
	Episode             int
	EpisodeID           string
	Network             string
	Status              string
	FirstAirDate        string
	LastAirDate         string
	NextAirDate         string
	EpisodeTitle        string
	EpisodeDescription  string
	EpisodeAirDate      string
	EpisodeImage        string
}

// SetGenericInfo writes non-zero Tier-1 fields into the entry's Fields map.
func (e *Entry) SetGenericInfo(info GenericInfo) {
	if info.Title != "" {
		e.Fields[FieldTitle] = info.Title
	}
	if info.Description != "" {
		e.Fields[FieldDescription] = info.Description
	}
}

// SetVideoInfo writes non-zero Tier-1 and Tier-2 fields into the entry's Fields map.
func (e *Entry) SetVideoInfo(info VideoInfo) {
	e.SetGenericInfo(info.GenericInfo)
	if info.Year > 0 {
		e.Fields[FieldYear] = info.Year
	}
	if info.PublishedDate != "" {
		e.Fields[FieldPublishedDate] = info.PublishedDate
	}
	if info.Language != "" {
		e.Fields[FieldLanguage] = info.Language
	}
	if info.OriginalTitle != "" {
		e.Fields[FieldOriginalTitle] = info.OriginalTitle
	}
	if info.Country != "" {
		e.Fields[FieldCountry] = info.Country
	}
	if len(info.Genres) > 0 {
		e.Fields[FieldGenres] = info.Genres
	}
	if info.Rating > 0 {
		e.Fields[FieldRating] = info.Rating
	}
	if info.Poster != "" {
		e.Fields[FieldPoster] = info.Poster
	}
	if len(info.Cast) > 0 {
		e.Fields[FieldCast] = info.Cast
	}
	if info.ContentRating != "" {
		e.Fields[FieldContentRating] = info.ContentRating
	}
	if info.Runtime > 0 {
		e.Fields[FieldRuntime] = info.Runtime
	}
	if len(info.Trailers) > 0 {
		e.Fields[FieldTrailers] = info.Trailers
	}
	if len(info.Aliases) > 0 {
		e.Fields[FieldAliases] = info.Aliases
	}
	if info.ImdbID != "" {
		e.Fields[FieldImdbID] = info.ImdbID
	}
}

// SetMovieInfo writes non-zero Tier-1 and Tier-2 fields into the entry's Fields map.
func (e *Entry) SetMovieInfo(info MovieInfo) {
	e.SetVideoInfo(info.VideoInfo)
}

// SetSeriesInfo writes non-zero Tier-1, Tier-2, and Tier-3 fields into the entry's Fields map.
func (e *Entry) SetSeriesInfo(info SeriesInfo) {
	e.SetVideoInfo(info.VideoInfo)
	if info.Season > 0 {
		e.Fields[FieldSeason] = info.Season
	}
	if info.Episode > 0 {
		e.Fields[FieldEpisode] = info.Episode
	}
	if info.EpisodeID != "" {
		e.Fields[FieldEpisodeID] = info.EpisodeID
	}
	if info.Network != "" {
		e.Fields[FieldNetwork] = info.Network
	}
	if info.Status != "" {
		e.Fields[FieldStatus] = info.Status
	}
	if info.FirstAirDate != "" {
		e.Fields[FieldFirstAirDate] = info.FirstAirDate
	}
	if info.LastAirDate != "" {
		e.Fields[FieldLastAirDate] = info.LastAirDate
	}
	if info.NextAirDate != "" {
		e.Fields[FieldNextAirDate] = info.NextAirDate
	}
	if info.EpisodeTitle != "" {
		e.Fields[FieldEpisodeTitle] = info.EpisodeTitle
	}
	if info.EpisodeDescription != "" {
		e.Fields[FieldEpisodeDescription] = info.EpisodeDescription
	}
	if info.EpisodeAirDate != "" {
		e.Fields[FieldEpisodeAirDate] = info.EpisodeAirDate
	}
	if info.EpisodeImage != "" {
		e.Fields[FieldEpisodeImage] = info.EpisodeImage
	}
}
