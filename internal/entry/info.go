package entry

import "time"

// Standard field name constants. Provider-specific fields (tvdb_*, tmdb_*, etc.)
// retain raw API values; these standard fields hold normalised, human-readable
// equivalents usable in conditions, pathfmt patterns, and templates.
//
// Naming convention: fields are prefixed by their info type (video_, series_,
// movie_, torrent_, file_, rss_) except for the three GenericInfo fields
// (title, description, published_date) which apply to every entry type.
const (
	// GenericInfo — no prefix, universal.
	FieldTitle         = "title"
	FieldDescription   = "description"
	FieldPublishedDate = "published_date"
	FieldEnriched      = "enriched" // true when an external metainfo provider successfully enriched this entry

	// VideoInfo — video_ prefix, shared by movies and series.
	FieldVideoYear          = "video_year"
	FieldVideoLanguage      = "video_language"
	FieldVideoOriginalTitle = "video_original_title"
	FieldVideoCountry       = "video_country"
	FieldVideoGenres        = "video_genres"
	FieldVideoRating        = "video_rating"
	FieldVideoPoster        = "video_poster"
	FieldVideoCast          = "video_cast"
	FieldVideoContentRating = "video_content_rating"
	FieldVideoRuntime       = "video_runtime"
	FieldVideoTrailers      = "video_trailers"
	FieldVideoAliases       = "video_aliases"
	FieldVideoImdbID        = "video_imdb_id"
	FieldVideoQuality       = "video_quality"
	FieldVideoResolution    = "video_resolution"
	FieldVideoSource        = "video_source"
	FieldVideoIs3D          = "video_is_3d"
	FieldVideoPopularity    = "video_popularity"
	FieldVideoVotes         = "video_votes"

	// MovieInfo — movie_ prefix.
	FieldMovieTitle   = "movie_title"
	FieldMovieTagline = "movie_tagline"

	// SeriesInfo — series_ prefix.
	FieldSeriesSeason             = "series_season"
	FieldSeriesEpisode            = "series_episode"
	FieldSeriesEpisodeID          = "series_episode_id"
	FieldSeriesNetwork            = "series_network"
	FieldSeriesStatus             = "series_status"
	FieldSeriesFirstAirDate       = "series_first_air_date"
	FieldSeriesLastAirDate        = "series_last_air_date"
	FieldSeriesNextAirDate        = "series_next_air_date"
	FieldSeriesEpisodeTitle       = "series_episode_title"
	FieldSeriesEpisodeDescription = "series_episode_description"
	FieldSeriesEpisodeAirDate     = "series_episode_air_date"
	FieldSeriesEpisodeImage       = "series_episode_image"
	FieldSeriesService            = "series_service"
	FieldSeriesProper             = "series_proper"
	FieldSeriesRepack             = "series_repack"
	FieldSeriesDoubleEpisode      = "series_double_episode"

	// TorrentInfo — torrent_ prefix.
	FieldTorrentInfoHash     = "torrent_info_hash"
	FieldTorrentFileSize     = "torrent_file_size"
	FieldTorrentFileCount    = "torrent_file_count"
	FieldTorrentFiles        = "torrent_files"
	FieldTorrentSeeds        = "torrent_seeds"
	FieldTorrentLeechers     = "torrent_leechers"
	FieldTorrentAnnounce     = "torrent_announce"
	FieldTorrentAnnounceList = "torrent_announce_list"
	FieldTorrentCreatedBy    = "torrent_created_by"
	FieldTorrentCreationDate = "torrent_creation_date"
	FieldTorrentPrivate      = "torrent_private"
	// FieldTorrentLinkType is set by sources that can determine the link type
	// without an HTTP fetch (e.g. Jackett via the Torznab magneturl attribute).
	// Values: "torrent" (URL serves a .torrent file) or "magnet" (URL is a
	// magnet URI). When absent, metainfo plugins fall back to URL inspection.
	FieldTorrentLinkType     = "torrent_link_type"

	// FileInfo — file_ prefix.
	FieldFileName         = "file_name"
	FieldFileExtension    = "file_extension"
	FieldFileLocation     = "file_location"
	FieldFileSize         = "file_size"
	FieldFileModifiedTime = "file_modified_time"

	// RSSInfo — rss_ prefix.
	FieldRSSFeed          = "rss_feed"
	FieldRSSGUID          = "rss_guid"
	FieldRSSLink          = "rss_link"
	FieldRSSEnclosureURL  = "rss_enclosure_url"
	FieldRSSEnclosureType = "rss_enclosure_type"
)

// --- Tier 1: Generic ---

// GenericInfo holds fields applicable to any entry type.
type GenericInfo struct {
	Title         string
	Description   string
	PublishedDate string
	Enriched      bool // set to true by external metainfo providers (TVDB, TMDb, Trakt, etc.)
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
	FirstAirDate       time.Time
	LastAirDate        time.Time
	NextAirDate        time.Time
	EpisodeTitle       string
	EpisodeDescription string
	EpisodeAirDate     time.Time
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
	if info.Enriched {
		e.Fields[FieldEnriched] = true
	}
}

// SetVideoInfo writes non-zero VideoInfo fields into the entry's Fields map.
func (e *Entry) SetVideoInfo(info VideoInfo) {
	e.SetGenericInfo(info.GenericInfo)
	if info.Year > 0 {
		e.Fields[FieldVideoYear] = info.Year
	}
	if info.Language != "" {
		e.Fields[FieldVideoLanguage] = info.Language
	}
	if info.OriginalTitle != "" {
		e.Fields[FieldVideoOriginalTitle] = info.OriginalTitle
	}
	if info.Country != "" {
		e.Fields[FieldVideoCountry] = info.Country
	}
	if len(info.Genres) > 0 {
		e.Fields[FieldVideoGenres] = info.Genres
	}
	if info.Rating > 0 {
		e.Fields[FieldVideoRating] = info.Rating
	}
	if info.Poster != "" {
		e.Fields[FieldVideoPoster] = info.Poster
	}
	if len(info.Cast) > 0 {
		e.Fields[FieldVideoCast] = info.Cast
	}
	if info.ContentRating != "" {
		e.Fields[FieldVideoContentRating] = info.ContentRating
	}
	if info.Runtime > 0 {
		e.Fields[FieldVideoRuntime] = info.Runtime
	}
	if len(info.Trailers) > 0 {
		e.Fields[FieldVideoTrailers] = info.Trailers
	}
	if len(info.Aliases) > 0 {
		e.Fields[FieldVideoAliases] = info.Aliases
	}
	if info.ImdbID != "" {
		e.Fields[FieldVideoImdbID] = info.ImdbID
	}
	if info.Quality != "" {
		e.Fields[FieldVideoQuality] = info.Quality
	}
	if info.Resolution != "" {
		e.Fields[FieldVideoResolution] = info.Resolution
	}
	if info.Source != "" {
		e.Fields[FieldVideoSource] = info.Source
	}
	if info.Is3D {
		e.Fields[FieldVideoIs3D] = true
	}
	if info.Popularity > 0 {
		e.Fields[FieldVideoPopularity] = info.Popularity
	}
	if info.Votes > 0 {
		e.Fields[FieldVideoVotes] = info.Votes
	}
}

// SetMovieInfo writes non-zero MovieInfo fields into the entry's Fields map.
func (e *Entry) SetMovieInfo(info MovieInfo) {
	e.SetVideoInfo(info.VideoInfo)
	if info.Title != "" {
		e.Fields[FieldMovieTitle] = info.Title
	}
	if info.Tagline != "" {
		e.Fields[FieldMovieTagline] = info.Tagline
	}
}

// SetSeriesInfo writes non-zero SeriesInfo fields into the entry's Fields map.
func (e *Entry) SetSeriesInfo(info SeriesInfo) {
	e.SetVideoInfo(info.VideoInfo)
	if info.Season > 0 {
		e.Fields[FieldSeriesSeason] = info.Season
	}
	if info.Episode > 0 {
		e.Fields[FieldSeriesEpisode] = info.Episode
	}
	if info.EpisodeID != "" {
		e.Fields[FieldSeriesEpisodeID] = info.EpisodeID
	}
	if info.Network != "" {
		e.Fields[FieldSeriesNetwork] = info.Network
	}
	if info.Status != "" {
		e.Fields[FieldSeriesStatus] = info.Status
	}
	if !info.FirstAirDate.IsZero() {
		e.Fields[FieldSeriesFirstAirDate] = info.FirstAirDate
	}
	if !info.LastAirDate.IsZero() {
		e.Fields[FieldSeriesLastAirDate] = info.LastAirDate
	}
	if !info.NextAirDate.IsZero() {
		e.Fields[FieldSeriesNextAirDate] = info.NextAirDate
	}
	if info.EpisodeTitle != "" {
		e.Fields[FieldSeriesEpisodeTitle] = info.EpisodeTitle
	}
	if info.EpisodeDescription != "" {
		e.Fields[FieldSeriesEpisodeDescription] = info.EpisodeDescription
	}
	if !info.EpisodeAirDate.IsZero() {
		e.Fields[FieldSeriesEpisodeAirDate] = info.EpisodeAirDate
	}
	if info.EpisodeImage != "" {
		e.Fields[FieldSeriesEpisodeImage] = info.EpisodeImage
	}
	if info.Service != "" {
		e.Fields[FieldSeriesService] = info.Service
	}
	if info.Proper {
		e.Fields[FieldSeriesProper] = true
	}
	if info.Repack {
		e.Fields[FieldSeriesRepack] = true
	}
	if info.DoubleEpisode > 0 {
		e.Fields[FieldSeriesDoubleEpisode] = info.DoubleEpisode
	}
}

// SetTorrentInfo writes non-zero TorrentInfo fields into the entry's Fields map.
func (e *Entry) SetTorrentInfo(info TorrentInfo) {
	e.SetGenericInfo(info.GenericInfo)
	if info.FileSize > 0 {
		e.Fields[FieldTorrentFileSize] = info.FileSize
	}
	if info.FileCount > 0 {
		e.Fields[FieldTorrentFileCount] = info.FileCount
	}
	if len(info.Files) > 0 {
		e.Fields[FieldTorrentFiles] = info.Files
	}
	if info.Seeds > 0 {
		e.Fields[FieldTorrentSeeds] = info.Seeds
	}
	if info.Leechers > 0 {
		e.Fields[FieldTorrentLeechers] = info.Leechers
	}
	if info.InfoHash != "" {
		e.Fields[FieldTorrentInfoHash] = info.InfoHash
	}
	if info.Announce != "" {
		e.Fields[FieldTorrentAnnounce] = info.Announce
	}
	if len(info.AnnounceList) > 0 {
		e.Fields[FieldTorrentAnnounceList] = info.AnnounceList
	}
	if info.CreatedBy != "" {
		e.Fields[FieldTorrentCreatedBy] = info.CreatedBy
	}
	if !info.CreationDate.IsZero() {
		e.Fields[FieldTorrentCreationDate] = info.CreationDate
	}
	if info.Private {
		e.Fields[FieldTorrentPrivate] = true
	}
}

// SetFileInfo writes non-zero FileInfo fields into the entry's Fields map.
func (e *Entry) SetFileInfo(info FileInfo) {
	e.SetGenericInfo(info.GenericInfo)
	if info.Filename != "" {
		e.Fields[FieldFileName] = info.Filename
	}
	if info.Extension != "" {
		e.Fields[FieldFileExtension] = info.Extension
	}
	if info.Location != "" {
		e.Fields[FieldFileLocation] = info.Location
	}
	if info.FileSize > 0 {
		e.Fields[FieldFileSize] = info.FileSize
	}
	if !info.ModifiedTime.IsZero() {
		e.Fields[FieldFileModifiedTime] = info.ModifiedTime
	}
}

// SetRSSInfo writes non-zero RSSInfo fields into the entry's Fields map.
func (e *Entry) SetRSSInfo(info RSSInfo) {
	e.SetGenericInfo(info.GenericInfo)
	if info.Feed != "" {
		e.Fields[FieldRSSFeed] = info.Feed
	}
	if info.GUID != "" {
		e.Fields[FieldRSSGUID] = info.GUID
	}
	if info.Link != "" {
		e.Fields[FieldRSSLink] = info.Link
	}
	if info.EnclosureURL != "" {
		e.Fields[FieldRSSEnclosureURL] = info.EnclosureURL
	}
	if info.EnclosureType != "" {
		e.Fields[FieldRSSEnclosureType] = info.EnclosureType
	}
}
