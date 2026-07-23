package entry

// FieldType describes the Go type of an entry field for UI purposes.
type FieldType string

const (
	FieldTypeString     FieldType = "string"
	FieldTypeInt        FieldType = "int"
	FieldTypeInt64      FieldType = "int64"
	FieldTypeFloat      FieldType = "float"
	FieldTypeBool       FieldType = "bool"
	FieldTypeStringList FieldType = "string_list"
	FieldTypeTime       FieldType = "time"
)

// FieldMeta describes a single known entry field.
type FieldMeta struct {
	Name        string    `json:"name"`
	Type        FieldType `json:"type"`
	Description string    `json:"description"`
	// SetBy lists the plugin names (or groups) that produce this field.
	SetBy []string `json:"set_by,omitempty"`
	// KnownValues lists the finite set of values this field can hold (if any).
	// The UI uses this to offer autocomplete on the value side of a condition.
	KnownValues []string `json:"known_values,omitempty"`
	// Deprecated marks the field as scheduled for removal. The DAG validator
	// surfaces a warning when a deprecated field is referenced in plugin
	// Requires, condition rules, route ports, or pathfmt patterns. The visual
	// editor renders deprecated names with a strikethrough and a hint.
	Deprecated bool `json:"deprecated,omitempty"`
	// ReplacedBy is the name of the field users should reference instead.
	// Surfaced in warnings and tooltips when non-empty.
	ReplacedBy string `json:"replaced_by,omitempty"`
	// DeprecationNote is an optional human-readable reason shown alongside the
	// warning. Used when the replacement isn't a single field name (e.g.
	// "use media_type == \"movie\" with title").
	DeprecationNote string `json:"deprecation_note,omitempty"`
}

// LookupField returns the FieldMeta for name, or (zero, false) if name is not a
// known field. The lookup is linear over KnownFields; callers that need it in
// a hot path should cache the result.
func LookupField(name string) (FieldMeta, bool) {
	for _, f := range KnownFields {
		if f.Name == name {
			return f, true
		}
	}
	return FieldMeta{}, false
}

// KnownFields is the authoritative registry of all standard entry fields.
// It drives the condition editor's field picker, operator selection,
// value widget, and type-mismatch warnings.
var KnownFields = []FieldMeta{
	// ── Universal ─────────────────────────────────────────────────────────
	{Name: FieldSource, Type: FieldTypeString,
		Description: "Origin of the entry in the form plugin:identifier (e.g. rss:nyaa.si, jackett:1337x)",
		SetBy:       []string{"rss", "jackett", "html", "filesystem", "trakt_list", "tvdb_favorites"}},
	{Name: FieldTitle, Type: FieldTypeString,
		Description: "Canonical display name (enriched by metainfo providers; use raw_title for the original)",
		SetBy:       []string{"all source plugins", "metainfo_tvdb", "metainfo_tmdb", "metainfo_trakt", "series", "movies"}},
	{Name: "raw_title", Type: FieldTypeString,
		Description: "Original entry title as received from the source, before enrichment (derived from entry URL/title, not from Fields)",
		SetBy:       []string{"all source plugins"}},
	{Name: FieldDescription, Type: FieldTypeString,
		Description: "Synopsis or overview text",
		SetBy:       []string{"rss", "metainfo_tvdb", "metainfo_tmdb", "metainfo_trakt"}},
	{Name: FieldPublishedDate, Type: FieldTypeString,
		Description: "Publication or release date string",
		SetBy:       []string{"rss", "jackett"}},
	{Name: FieldEnriched, Type: FieldTypeBool,
		Description: "true when an external metainfo provider (TVDB, TMDb, Trakt) has enriched this entry",
		SetBy:       []string{"metainfo_tvdb", "metainfo_tmdb", "metainfo_trakt"}},

	// ── Video (shared by movies and series) ────────────────────────────────
	{Name: FieldVideoYear, Type: FieldTypeInt,
		Description: "Release or first-air year",
		SetBy:       []string{"metainfo_tvdb", "metainfo_tmdb", "metainfo_trakt", "movies", "trakt_list"}},
	{Name: FieldVideoLanguage, Type: FieldTypeString,
		Description: "Original language display name (e.g. English, Japanese)",
		SetBy:       []string{"metainfo_tvdb", "metainfo_tmdb", "metainfo_trakt"}},
	{Name: FieldVideoOriginalTitle, Type: FieldTypeString,
		Description: "Original title in the native language",
		SetBy:       []string{"metainfo_tmdb", "metainfo_trakt"}},
	{Name: FieldVideoCountry, Type: FieldTypeString,
		Description: "Production country",
		SetBy:       []string{"metainfo_tvdb", "metainfo_tmdb"}},
	{Name: FieldVideoGenres, Type: FieldTypeStringList,
		Description: "Genre list (e.g. [\"Action\", \"Drama\"])",
		SetBy:       []string{"metainfo_tvdb", "metainfo_tmdb", "metainfo_trakt"}},
	{Name: FieldVideoRating, Type: FieldTypeFloat,
		Description: "Rating on a 0–10 scale",
		SetBy:       []string{"metainfo_tvdb", "metainfo_tmdb", "metainfo_trakt"}},
	{Name: FieldVideoPopularity, Type: FieldTypeFloat,
		Description: "Popularity score from the metadata provider",
		SetBy:       []string{"metainfo_tmdb", "metainfo_trakt"}},
	{Name: FieldVideoVotes, Type: FieldTypeInt,
		Description: "Number of votes contributing to the rating",
		SetBy:       []string{"metainfo_tmdb", "metainfo_trakt"}},
	{Name: FieldVideoPoster, Type: FieldTypeString,
		Description: "Poster image URL",
		SetBy:       []string{"metainfo_tvdb", "metainfo_tmdb", "metainfo_trakt"}},
	{Name: FieldVideoCast, Type: FieldTypeStringList,
		Description: "List of cast member names",
		SetBy:       []string{"metainfo_tmdb", "metainfo_trakt"}},
	{Name: FieldVideoContentRating, Type: FieldTypeString,
		Description: "Content rating (e.g. TV-MA, PG-13)",
		SetBy:       []string{"metainfo_tvdb", "metainfo_tmdb"}},
	{Name: FieldVideoRuntime, Type: FieldTypeInt,
		Description: "Runtime in minutes",
		SetBy:       []string{"metainfo_tmdb", "metainfo_trakt"}},
	{Name: FieldVideoTrailers, Type: FieldTypeStringList,
		Description: "List of trailer URLs",
		SetBy:       []string{"metainfo_tmdb"}},
	{Name: FieldVideoAliases, Type: FieldTypeStringList,
		Description: "Alternative titles",
		SetBy:       []string{"metainfo_tvdb", "metainfo_tmdb"}},
	{Name: FieldVideoImdbID, Type: FieldTypeString,
		Description: "IMDb ID (e.g. tt0903747)",
		SetBy:       []string{"metainfo_tmdb", "metainfo_trakt"}},
	{Name: FieldVideoQuality, Type: FieldTypeString,
		Description: "Parsed quality string (e.g. 1080p BluRay)",
		SetBy:       []string{"metainfo_file", "movies"}},
	{Name: FieldVideoResolution, Type: FieldTypeString,
		Description: "Resolution tag (e.g. 1080p, 4K)",
		SetBy:       []string{"metainfo_file", "movies"},
		KnownValues: []string{"4K", "2160p", "1080p", "720p", "576p", "480p"}},
	{Name: FieldVideoSource, Type: FieldTypeString,
		Description: "Release source tag (e.g. BluRay, WEBRip)",
		SetBy:       []string{"metainfo_file"},
		KnownValues: []string{"BluRay", "WEBRip", "WEB-DL", "HDTV", "DVDRip", "CAM", "TS"}},
	{Name: FieldVideoIs3D, Type: FieldTypeBool,
		Description: "true when the release is 3D",
		SetBy:       []string{"metainfo_file"}},
	{Name: FieldVideoProper, Type: FieldTypeBool,
		Description: "true when the release is tagged PROPER (corrected re-release)",
		SetBy:       []string{"metainfo_file"}},
	{Name: FieldVideoRepack, Type: FieldTypeBool,
		Description: "true when the release is tagged REPACK",
		SetBy:       []string{"metainfo_file"}},

	// ── Movie-specific ────────────────────────────────────────────────────
	{Name: FieldMovieTitle, Type: FieldTypeString,
		Description:     "Canonical movie title from the metadata provider",
		SetBy:           []string{"movies", "metainfo_tmdb"},
		Deprecated:      true,
		ReplacedBy:      FieldTitle,
		DeprecationNote: `duplicates "title" whenever "media_type" == "movie"; use "title" (and check "media_type" if you need to disambiguate movies from series)`,
	},
	{Name: FieldMovieTagline, Type: FieldTypeString,
		Description: "Movie tagline",
		SetBy:       []string{"metainfo_tmdb"}},

	// ── Series-specific ───────────────────────────────────────────────────
	{Name: FieldSeriesSeason, Type: FieldTypeInt,
		Description: "Season number",
		SetBy:       []string{"metainfo_file", "metainfo_tvdb", "jackett"}},
	{Name: FieldSeriesEpisode, Type: FieldTypeInt,
		Description: "Episode number within the season",
		SetBy:       []string{"metainfo_file", "metainfo_tvdb", "jackett"}},
	{Name: FieldSeriesEpisodeID, Type: FieldTypeString,
		Description: "Episode ID in SnnEnn format (e.g. S02E05) — used as dedup key",
		SetBy:       []string{"metainfo_file", "metainfo_tvdb"}},
	{Name: FieldSeriesNetwork, Type: FieldTypeString,
		Description: "Broadcast or streaming network",
		SetBy:       []string{"metainfo_tvdb", "metainfo_trakt"}},
	{Name: FieldSeriesStatus, Type: FieldTypeString,
		Description: "Series status. TheTVDB sources use capitalised values (Continuing, Ended, Cancelled, Upcoming); Trakt sources use lowercase ones (returning series, ended, canceled)",
		SetBy:       []string{"metainfo_tvdb", "metainfo_trakt", "tvdb_favorites", "trakt_list"},
		KnownValues: []string{"Continuing", "Ended", "Cancelled", "Upcoming", "Pilot"}},
	{Name: FieldSeriesFirstAirDate, Type: FieldTypeTime,
		Description: "Date of the first aired episode",
		SetBy:       []string{"metainfo_tvdb", "metainfo_trakt"}},
	{Name: FieldSeriesLastAirDate, Type: FieldTypeTime,
		Description: "Date of the most recently aired episode",
		SetBy:       []string{"metainfo_tvdb", "metainfo_trakt"}},
	{Name: FieldSeriesNextAirDate, Type: FieldTypeTime,
		Description: "Expected air date of the next episode",
		SetBy:       []string{"metainfo_tvdb", "metainfo_trakt"}},
	{Name: FieldSeriesEpisodeTitle, Type: FieldTypeString,
		Description: "Episode title",
		SetBy:       []string{"metainfo_tvdb", "metainfo_trakt"}},
	{Name: FieldSeriesEpisodeDescription, Type: FieldTypeString,
		Description: "Episode overview or description",
		SetBy:       []string{"metainfo_tvdb", "metainfo_trakt"}},
	{Name: FieldSeriesEpisodeAirDate, Type: FieldTypeTime,
		Description: "Air date of this specific episode",
		SetBy:       []string{"metainfo_tvdb", "metainfo_trakt"}},
	{Name: FieldSeriesEpisodeImage, Type: FieldTypeString,
		Description: "Still image URL for this episode",
		SetBy:       []string{"metainfo_tvdb"}},
	{Name: FieldSeriesService, Type: FieldTypeString,
		Description: "Streaming service the show is available on",
		SetBy:       []string{"metainfo_trakt"}},
	{Name: FieldSeriesDoubleEpisode, Type: FieldTypeInt,
		Description: "Second episode number when this is a double-episode release",
		SetBy:       []string{"metainfo_file"}},

	// ── Torrent ──────────────────────────────────────────────────────────
	{Name: FieldTorrentInfoHash, Type: FieldTypeString,
		Description: "SHA-1 info hash (lowercase hex)",
		SetBy:       []string{"rss", "jackett", "metainfo_torrent", "metainfo_magnet"}},
	{Name: FieldTorrentFileSize, Type: FieldTypeInt64,
		Description: "Total torrent size in bytes",
		SetBy:       []string{"jackett", "metainfo_torrent", "metainfo_magnet"}},
	{Name: FieldTorrentFileCount, Type: FieldTypeInt,
		Description: "Number of files inside the torrent",
		SetBy:       []string{"metainfo_torrent", "metainfo_magnet"}},
	{Name: FieldTorrentFiles, Type: FieldTypeStringList,
		Description: "List of file paths inside the torrent",
		SetBy:       []string{"metainfo_torrent", "metainfo_magnet"}},
	{Name: FieldTorrentSeeds, Type: FieldTypeInt,
		Description: "Seeder count",
		SetBy:       []string{"rss", "jackett"}},
	{Name: FieldTorrentLeechers, Type: FieldTypeInt,
		Description: "Leecher count",
		SetBy:       []string{"rss", "jackett"}},
	{Name: FieldTorrentGrabs, Type: FieldTypeInt,
		Description: "Number of times downloaded from the indexer",
		SetBy:       []string{"rss", "jackett"}},
	{Name: FieldTorrentAnnounce, Type: FieldTypeString,
		Description: "Primary tracker announce URL",
		SetBy:       []string{"metainfo_torrent", "metainfo_magnet"}},
	{Name: FieldTorrentAnnounceList, Type: FieldTypeStringList,
		Description: "Full tracker announce list",
		SetBy:       []string{"metainfo_torrent", "metainfo_magnet"}},
	{Name: FieldTorrentCreatedBy, Type: FieldTypeString,
		Description: "Client that created the torrent",
		SetBy:       []string{"metainfo_torrent"}},
	{Name: FieldTorrentCreationDate, Type: FieldTypeTime,
		Description: "Torrent file creation date",
		SetBy:       []string{"metainfo_torrent"}},
	{Name: FieldTorrentPrivate, Type: FieldTypeBool,
		Description: "true when the torrent is marked private",
		SetBy:       []string{"metainfo_torrent"}},
	{Name: FieldTorrentLinkType, Type: FieldTypeString,
		Description: "\"torrent\" or \"magnet\" — set by Jackett/RSS to indicate the link type without an HTTP fetch",
		SetBy:       []string{"rss", "jackett"},
		KnownValues: []string{"torrent", "magnet"}},

	// ── File ──────────────────────────────────────────────────────────────
	{Name: FieldFileName, Type: FieldTypeString,
		Description: "Filename including extension",
		SetBy:       []string{"filesystem"}},
	{Name: FieldFileExtension, Type: FieldTypeString,
		Description: "File extension including the leading dot (e.g. .torrent)",
		SetBy:       []string{"filesystem"}},
	{Name: FieldFileLocation, Type: FieldTypeString,
		Description: "Absolute path to the file",
		SetBy:       []string{"filesystem"}},
	{Name: FieldFileSize, Type: FieldTypeInt64,
		Description: "File size in bytes",
		SetBy:       []string{"filesystem"}},
	{Name: FieldFileModifiedTime, Type: FieldTypeTime,
		Description: "File last-modified timestamp",
		SetBy:       []string{"filesystem"}},

	// ── RSS ───────────────────────────────────────────────────────────────
	{Name: FieldRSSFeed, Type: FieldTypeString,
		Description: "Feed URL used to fetch this entry",
		SetBy:       []string{"rss"}},
	{Name: FieldRSSGUID, Type: FieldTypeString,
		Description: "Item GUID from the feed",
		SetBy:       []string{"rss"}},
	{Name: FieldRSSLink, Type: FieldTypeString,
		Description: "Item link element value",
		SetBy:       []string{"rss"}},
	{Name: FieldRSSEnclosureURL, Type: FieldTypeString,
		Description: "Enclosure URL",
		SetBy:       []string{"rss"}},
	{Name: FieldRSSEnclosureType, Type: FieldTypeString,
		Description: "Enclosure MIME type",
		SetBy:       []string{"rss"}},
	{Name: FieldRSSCategory, Type: FieldTypeString,
		Description: "Category from RSS <category> elements or nyaa:category, comma-joined",
		SetBy:       []string{"rss"}},

	// ── Classification ────────────────────────────────────────────────────
	{Name: FieldMediaType, Type: FieldTypeString,
		Description: "Media classification: \"series\", \"movie\", or unset. Set conditionally by metainfo plugins (when classification succeeds) and unconditionally by sources that only catalog one type (tvdb_favorites, trakt_list) and by classifier filters (every entry exiting series/movies/premiere carries this field).",
		SetBy:       []string{"trakt_list", "tvdb_favorites", "metainfo_file", "metainfo_tmdb", "metainfo_tvdb", "metainfo_trakt", "series", "movies", "premiere"},
		KnownValues: []string{MediaTypeSeries, MediaTypeMovie}},

	// ── Route ─────────────────────────────────────────────────────────────
	{Name: FieldRoutePort, Type: FieldTypeString,
		Description: "Port name stamped by route() — identifies which branch this entry took",
		SetBy:       []string{"route"}},

	// ── report_empty ──────────────────────────────────────────────────────
	{Name: FieldEmptyMarker, Type: FieldTypeBool,
		Description: "True on the synthetic marker entry emitted by report_empty when its upstream was empty",
		SetBy:       []string{"report_empty"}},

	// ── Jackett-specific ──────────────────────────────────────────────────
	{Name: "jackett_category", Type: FieldTypeString,
		Description: "Torznab category code from Jackett",
		SetBy:       []string{"jackett"}},
	{Name: "jackett_imdb_id", Type: FieldTypeString,
		Description: "Raw IMDb ID from Jackett — same pattern as trakt_imdb_id, usable for fast metainfo_tmdb/metainfo_trakt lookups",
		SetBy:       []string{"jackett"}},
	{Name: "jackett_tvdb_id", Type: FieldTypeString,
		Description: "Raw TVDB ID from Jackett — same pattern as trakt_tvdb_id, usable for fast metainfo_tvdb lookups",
		SetBy:       []string{"jackett"}},
	{Name: "jackett_tmdb_id", Type: FieldTypeString,
		Description: "Raw TMDb ID from Jackett — same pattern as trakt_tmdb_id",
		SetBy:       []string{"jackett"}},
	{Name: "jackett_dl_factor", Type: FieldTypeFloat,
		Description: "Download volume factor: 0.0 = freeleech, 0.5 = half-leech, 1.0 = normal",
		SetBy:       []string{"jackett"}},
	{Name: "jackett_ul_factor", Type: FieldTypeFloat,
		Description: "Upload volume factor for private trackers",
		SetBy:       []string{"jackett"}},

	// ── Provider-specific IDs ─────────────────────────────────────────────
	{Name: "tvdb_id", Type: FieldTypeInt,
		Description: "TheTVDB series ID",
		SetBy:       []string{"tvdb_favorites", "metainfo_tvdb"}},
	{Name: "tvdb_year", Type: FieldTypeString,
		Description: "Premiere year from TheTVDB",
		SetBy:       []string{"tvdb_favorites", "metainfo_tvdb"}},
	{Name: "tvdb_slug", Type: FieldTypeString,
		Description: "URL slug for thetvdb.com/series/{slug}",
		SetBy:       []string{"metainfo_tvdb"}},
	{Name: "tvdb_episode_id", Type: FieldTypeInt,
		Description: "TheTVDB episode ID",
		SetBy:       []string{"metainfo_tvdb"}},
	{Name: "tmdb_id", Type: FieldTypeInt,
		Description: "TMDb movie or series ID",
		SetBy:       []string{"metainfo_tmdb"}},
	{Name: "trakt_id", Type: FieldTypeInt,
		Description: "Trakt internal ID",
		SetBy:       []string{"trakt_list", "metainfo_trakt"}},
	{Name: "trakt_slug", Type: FieldTypeString,
		Description: "Trakt URL slug",
		SetBy:       []string{"trakt_list", "metainfo_trakt"}},
	{Name: "trakt_imdb_id", Type: FieldTypeString,
		Description: "IMDb ID from Trakt",
		SetBy:       []string{"trakt_list"}},
	{Name: "trakt_tmdb_id", Type: FieldTypeInt,
		Description: "TMDb ID from Trakt — when present, metainfo_tmdb fetches by ID directly",
		SetBy:       []string{"trakt_list", "metainfo_trakt"}},
	{Name: "trakt_tvdb_id", Type: FieldTypeInt,
		Description: "TVDB ID from Trakt",
		SetBy:       []string{"metainfo_trakt"}},
	{Name: "trakt_tvdb_id", Type: FieldTypeInt,
		Description: "TVDB ID from Trakt",
		SetBy:       []string{"metainfo_trakt"}},

	// ── Quality sub-fields ────────────────────────────────────────────────
	{Name: "codec", Type: FieldTypeString,
		Description: "Codec tag (e.g. x265, HEVC)",
		SetBy:       []string{"metainfo_file"},
		KnownValues: []string{"x264", "x265", "HEVC", "AV1", "xvid", "H.264", "H.265"}},
	{Name: "audio", Type: FieldTypeString,
		Description: "Audio tag (e.g. DTS, Atmos)",
		SetBy:       []string{"metainfo_file"},
		KnownValues: []string{"AAC", "AC3", "DTS", "DTS-HD", "TrueHD", "Atmos", "MP3", "FLAC"}},
	{Name: "color_range", Type: FieldTypeString,
		Description: "Color range tag (e.g. HDR, SDR, Dolby Vision)",
		SetBy:       []string{"metainfo_file"},
		KnownValues: []string{"HDR", "HDR10", "HDR10+", "Dolby Vision", "SDR"}},
	{Name: "quality_resolution", Type: FieldTypeString,
		Description: "Resolution sub-field (integer enum as string)",
		SetBy:       []string{"metainfo_file"}},
	{Name: "quality_source", Type: FieldTypeString,
		Description: "Source sub-field (integer enum as string)",
		SetBy:       []string{"metainfo_file"}},
	{Name: FieldQuality, Type: FieldTypeString,
		Description: "Internal: parsed quality.Quality struct for typed downstream consumers. Not directly readable from conditions/templates — use the video_quality / video_resolution / video_source string fields for those.",
		SetBy:       []string{"metainfo_file"}},

	// ── Misc ─────────────────────────────────────────────────────────────
	{Name: "series_container", Type: FieldTypeString,
		Description: "Container format tag parsed from title (e.g. mkv)",
		SetBy:       []string{"series"}},
	{Name: "download_path", Type: FieldTypeString,
		Description: "Rendered destination path set by pathfmt — read by output plugins",
		SetBy:       []string{"pathfmt"}},
}

// KnownFieldMap is a pre-built lookup table from field name to metadata.
// Populated at init time from KnownFields.
var KnownFieldMap map[string]FieldMeta

func init() {
	KnownFieldMap = make(map[string]FieldMeta, len(KnownFields))
	for _, f := range KnownFields {
		KnownFieldMap[f.Name] = f
	}
}

