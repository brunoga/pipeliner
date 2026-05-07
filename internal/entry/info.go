package entry

import "time"

// Standard field name constants. Provider-specific fields (tvdb_*, tmdb_*, etc.)
// retain raw API values; these standard fields hold normalised, human-readable
// equivalents usable in conditions, pathfmt patterns, and templates.
const (
	// Tier 1 — Generic (any entry type).
	FieldTitle         = "title"
	FieldDescription   = "description"
	FieldPublishedDate = "published_date"

	// Tier 2 — Video (shared by movies and series).
	FieldYear          = "year"
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
	FieldQuality       = "quality"
	FieldResolution    = "resolution"
	FieldSource        = "source"
	FieldIs3D          = "is_3d"
	FieldPopularity    = "popularity"
	FieldVotes         = "votes"

	// Tier 3a — Movie only.
	FieldTagline = "tagline"

	// Tier 3b — Series only.
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
	FieldService            = "service"
	FieldProper             = "proper"
	FieldRepack             = "repack"
	FieldDoubleEpisode      = "double_episode"

	// Torrent fields.
	FieldInfoHash      = "info_hash"
	FieldFileSize      = "file_size"
	FieldFileCount     = "file_count"
	FieldFiles         = "files"
	FieldSeeds         = "seeds"
	FieldLeechers      = "leechers"
	FieldAnnounce      = "announce"
	FieldAnnounceList  = "announce_list"
	FieldCreatedBy     = "created_by"
	FieldCreationDate  = "creation_date"
	FieldPrivate       = "private"

	// File fields.
	FieldFilename     = "filename"
	FieldExtension    = "extension"
	FieldLocation     = "location"
	FieldModifiedTime = "modified_time"

	// RSS fields.
	FieldFeed           = "feed"
	FieldGUID           = "guid"
	FieldLink           = "link"
	FieldEnclosureURL   = "enclosure_url"
	FieldEnclosureType  = "enclosure_type"
)

// --- Tier 1: Generic ---

// GenericInfo holds fields applicable to any entry type.
type GenericInfo struct {
	Title         string
	Description   string
	PublishedDate string
}

// --- Tier 2: Video ---

// VideoInfo holds fields shared by all video content (movies and series).
type VideoInfo struct {
	GenericInfo
	Year          int
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
	Quality       string
	Resolution    string
	Source        string
	Is3D          bool
	Popularity    float64
	Votes         int
}

// --- Tier 3a: Movie ---

// MovieInfo holds movie-specific fields on top of VideoInfo.
type MovieInfo struct {
	VideoInfo
	Tagline string
}

// --- Tier 3b: Series ---

// SeriesInfo holds series-specific fields on top of VideoInfo.
type SeriesInfo struct {
	VideoInfo
	Season             int
	Episode            int
	EpisodeID          string
	Network            string
	Status             string
	FirstAirDate       string
	LastAirDate        string
	NextAirDate        string
	EpisodeTitle       string
	EpisodeDescription string
	EpisodeAirDate     string
	EpisodeImage       string
	Service            string
	Proper             bool
	Repack             bool
	DoubleEpisode      int
}

// --- Leaf: Torrent ---

// TorrentInfo holds all BitTorrent-specific metadata.
type TorrentInfo struct {
	GenericInfo
	FileSize     int64
	FileCount    int
	Files        []string
	Seeds        int
	Leechers     int
	InfoHash     string
	Announce     string
	AnnounceList []string
	CreatedBy    string
	CreationDate time.Time
	Private      bool
}

// --- Leaf: File ---

// FileInfo holds filesystem entry metadata.
type FileInfo struct {
	GenericInfo
	Filename     string
	Extension    string
	Location     string
	FileSize     int64
	ModifiedTime time.Time
}

// --- Leaf: RSS ---

// RSSInfo holds RSS/Atom feed entry metadata.
type RSSInfo struct {
	GenericInfo
	Feed          string
	GUID          string
	Link          string
	EnclosureURL  string
	EnclosureType string
}

// --- Setters ---

// SetGenericInfo writes non-zero Tier-1 fields into the entry's Fields map.
func (e *Entry) SetGenericInfo(info GenericInfo) {
	if info.Title != "" {
		e.Fields[FieldTitle] = info.Title
	}
	if info.Description != "" {
		e.Fields[FieldDescription] = info.Description
	}
	if info.PublishedDate != "" {
		e.Fields[FieldPublishedDate] = info.PublishedDate
	}
}

// SetVideoInfo writes non-zero Tier-1 and Tier-2 fields into the entry's Fields map.
func (e *Entry) SetVideoInfo(info VideoInfo) {
	e.SetGenericInfo(info.GenericInfo)
	if info.Year > 0 {
		e.Fields[FieldYear] = info.Year
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
	if info.Quality != "" {
		e.Fields[FieldQuality] = info.Quality
	}
	if info.Resolution != "" {
		e.Fields[FieldResolution] = info.Resolution
	}
	if info.Source != "" {
		e.Fields[FieldSource] = info.Source
	}
	if info.Is3D {
		e.Fields[FieldIs3D] = true
	}
	if info.Popularity > 0 {
		e.Fields[FieldPopularity] = info.Popularity
	}
	if info.Votes > 0 {
		e.Fields[FieldVotes] = info.Votes
	}
}

// SetMovieInfo writes non-zero Tier-1, Tier-2, and movie fields into the entry's Fields map.
func (e *Entry) SetMovieInfo(info MovieInfo) {
	e.SetVideoInfo(info.VideoInfo)
	if info.Tagline != "" {
		e.Fields[FieldTagline] = info.Tagline
	}
}

// SetSeriesInfo writes non-zero Tier-1, Tier-2, and series fields into the entry's Fields map.
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
	if info.Service != "" {
		e.Fields[FieldService] = info.Service
	}
	if info.Proper {
		e.Fields[FieldProper] = true
	}
	if info.Repack {
		e.Fields[FieldRepack] = true
	}
	if info.DoubleEpisode > 0 {
		e.Fields[FieldDoubleEpisode] = info.DoubleEpisode
	}
}

// SetTorrentInfo writes non-zero torrent fields into the entry's Fields map.
func (e *Entry) SetTorrentInfo(info TorrentInfo) {
	e.SetGenericInfo(info.GenericInfo)
	if info.FileSize > 0 {
		e.Fields[FieldFileSize] = info.FileSize
	}
	if info.FileCount > 0 {
		e.Fields[FieldFileCount] = info.FileCount
	}
	if len(info.Files) > 0 {
		e.Fields[FieldFiles] = info.Files
	}
	if info.Seeds > 0 {
		e.Fields[FieldSeeds] = info.Seeds
	}
	if info.Leechers > 0 {
		e.Fields[FieldLeechers] = info.Leechers
	}
	if info.InfoHash != "" {
		e.Fields[FieldInfoHash] = info.InfoHash
	}
	if info.Announce != "" {
		e.Fields[FieldAnnounce] = info.Announce
	}
	if len(info.AnnounceList) > 0 {
		e.Fields[FieldAnnounceList] = info.AnnounceList
	}
	if info.CreatedBy != "" {
		e.Fields[FieldCreatedBy] = info.CreatedBy
	}
	if !info.CreationDate.IsZero() {
		e.Fields[FieldCreationDate] = info.CreationDate
	}
	if info.Private {
		e.Fields[FieldPrivate] = true
	}
}

// SetFileInfo writes non-zero filesystem fields into the entry's Fields map.
func (e *Entry) SetFileInfo(info FileInfo) {
	e.SetGenericInfo(info.GenericInfo)
	if info.Filename != "" {
		e.Fields[FieldFilename] = info.Filename
	}
	if info.Extension != "" {
		e.Fields[FieldExtension] = info.Extension
	}
	if info.Location != "" {
		e.Fields[FieldLocation] = info.Location
	}
	if info.FileSize > 0 {
		e.Fields[FieldFileSize] = info.FileSize
	}
	if !info.ModifiedTime.IsZero() {
		e.Fields[FieldModifiedTime] = info.ModifiedTime
	}
}

// SetRSSInfo writes non-zero RSS fields into the entry's Fields map.
func (e *Entry) SetRSSInfo(info RSSInfo) {
	e.SetGenericInfo(info.GenericInfo)
	if info.Feed != "" {
		e.Fields[FieldFeed] = info.Feed
	}
	if info.GUID != "" {
		e.Fields[FieldGUID] = info.GUID
	}
	if info.Link != "" {
		e.Fields[FieldLink] = info.Link
	}
	if info.EnclosureURL != "" {
		e.Fields[FieldEnclosureURL] = info.EnclosureURL
	}
	if info.EnclosureType != "" {
		e.Fields[FieldEnclosureType] = info.EnclosureType
	}
}
