// Package bluray is a Blu-ray.com client and parser used by the
// bluray_releases source plugin and the metainfo_bluray processor plugin.
package bluray

import "time"

// Format identifies which physical media a Blu-ray.com release record describes.
type Format string

const (
	FormatBD   Format = "BD"   // standard Blu-ray Disc
	FormatUHD  Format = "UHD"  // 4K Ultra HD Blu-ray
	FormatBD3D Format = "BD3D" // 3D Blu-ray (frame-packed MVC or Anaglyph 3D)
	FormatDVD  Format = "DVD"  // DVD-Video
)

// Country represents a country/region filter on blu-ray.com.
type Country string

const (
	CountryAll Country = "all"
	CountryUS  Country = "us"
	CountryUK  Country = "uk"
	CountryCA  Country = "ca"
	CountryDE  Country = "de"
	CountryFR  Country = "fr"
	CountryIT  Country = "it"
	CountryES  Country = "es"
	CountryJP  Country = "jp"
	CountryAU  Country = "au"
	CountryKR  Country = "kr"
	CountryHK  Country = "hk"
	CountryMX  Country = "mx"
	CountryBR  Country = "br"
	CountryNL  Country = "nl"
	CountrySE  Country = "se"
	CountryNO  Country = "no"
	CountryDK  Country = "dk"
	CountryFI  Country = "fi"
	CountryPL  Country = "pl"
	CountryCZ  Country = "cz"
	CountryIN  Country = "in"
	CountryTW  Country = "tw"
	CountryCN  Country = "cn"
)

// AllCountries lists all supported Country values for configuration validation.
var AllCountries = []string{
	string(CountryAll),
	string(CountryUS),
	string(CountryUK),
	string(CountryCA),
	string(CountryDE),
	string(CountryFR),
	string(CountryIT),
	string(CountryES),
	string(CountryJP),
	string(CountryAU),
	string(CountryKR),
	string(CountryHK),
	string(CountryMX),
	string(CountryBR),
	string(CountryNL),
	string(CountrySE),
	string(CountryNO),
	string(CountryDK),
	string(CountryFI),
	string(CountryPL),
	string(CountryCZ),
	string(CountryIN),
	string(CountryTW),
	string(CountryCN),
}

// SearchSection represents the section in quicksearch / autocomplete queries.
type SearchSection string

const (
	SectionBluRay     SearchSection = "bluraymovies"
	Section3D         SearchSection = "3d"
	Section4K         SearchSection = "4k"
	SectionTheatrical SearchSection = "theatrical"
	SectionDVD        SearchSection = "dvdmovies"
	SectionDigital    SearchSection = "uvmovies"
	SectionMA         SearchSection = "mamovies"
	SectioniTunes     SearchSection = "itunesmovies"
	SectionPrime      SearchSection = "aivmovies"
	SectionGames      SearchSection = "games"
	SectionPeople     SearchSection = "castandcrew"
)

// SortBy specifies the sort order for search queries.
type SortBy string

const (
	SortRelevance          SortBy = "relevance"
	SortTitle              SortBy = "title"
	SortTitleChronological SortBy = "titlechronological"
	SortRating             SortBy = "rating"
	SortStudio             SortBy = "studio"
	SortReleaseDate        SortBy = "releasetimestamp"
	SortReleaseDateDesc    SortBy = "releasetimestampdesc"
	SortRecentlyAdded      SortBy = "recentlyadded"
	SortRecentlyAddedDesc  SortBy = "recentlyaddeddesc"
	SortPreOrder           SortBy = "preordertimestamp"
	SortProductionYear     SortBy = "productionyear"
	SortProductionYearDesc SortBy = "productionyeardesc"
	SortRuntime            SortBy = "runtime"
	SortRuntimeDesc        SortBy = "runtimedesc"
	SortPopularity         SortBy = "popularity"
	SortPopularityDesc     SortBy = "popularitydesc"
	SortCollections        SortBy = "collections"
	SortCollectionsDesc    SortBy = "collectionsdesc"
	SortBestDeal           SortBy = "bestdeal"
	SortSalesRank          SortBy = "salesrank"
	SortLowestPrice        SortBy = "price"
	SortSavePercent        SortBy = "percent"
)

// SearchQuery defines criteria for searching Blu-ray releases via /movies/search.php.
type SearchQuery struct {
	Keyword     string
	UPC         string // 12-digit UPC barcode
	EAN         string // 13-digit EAN barcode
	ASIN        string // 10-character Amazon ASIN
	BluRay3D    bool   // Filter to Blu-ray 3D releases (other_bluray3d=1)
	Anaglyph3D  bool   // Filter to Anaglyph 3D releases (other_anaglyph3d=1)
	UltraHD4K   bool   // Filter to 4K Ultra HD releases (ultrahd=1)
	HDR10       bool
	DolbyVision bool
	YearFrom    int
	YearTo      int
	ReleaseYear int
	Audio       string
	Subtitles   string
	Synopsis    string
	BoxSet      bool
	Television  bool
	Genres      []string
	SortBy      SortBy
	Page        int     // 0-indexed page number (0 is the first page)
	Country     Country // Optional override for this query
}

// SearchResultPage represents a paginated page of Blu-ray search results.
type SearchResultPage struct {
	TotalMatches int
	CurrentPage  int
	HasNextPage  bool
	Country      Country
	Results      []ReleaseSummary
}

// ReleaseSummary represents a Blu-ray release item in search results or edition listings.
type ReleaseSummary struct {
	ID            string   // Product ID in URL (e.g., "56292")
	GlobalID      string   // Global Product ID if available
	ParentMovieID string   // Master movie ID if available (e.g., "128351")
	Title         string   // e.g., "Dredd 3D Blu-ray" or "Dredd 3D"
	Edition       string   // e.g., "Limited Edition / SteelBook"
	FormatSummary string   // e.g., "Blu-ray 3D + Blu-ray"
	URL           string   // e.g., "https://www.blu-ray.com/movies/Dredd-3D-Blu-ray/56292/"
	CoverURL      string   // Cover image URL
	Country       Country  // Country code (e.g., "us", "uk")
	Studio        string   // e.g., "Lionsgate Films"
	Year          int      // Production year, e.g., 2012
	RuntimeMins   int      // Runtime in minutes, e.g., 96
	Rated         string   // MPAA rating, e.g., "Rated R"
	RegionSummary string   // e.g., "2K Blu-ray: Region A (locked)"
	ReleaseDate   string   // Raw release date string, e.g., "Jan 08, 2013" or "2013-01-08"
	OverallRating float64  // Overall score out of 5.0 if rated
	Price         string   // Current price string if listed (e.g., "$8.73")
	Synopsis      string   // Short synopsis from full search layout
	Genres        []string // Genre list from full search layout
	Is3D          bool     // True if Blu-ray 3D is indicated in title, format, or URL
	Is4K          bool     // True if 4K Ultra HD is indicated
}

// Format returns the primary Format (BD3D, UHD, DVD, or BD) of this summary.
func (rs ReleaseSummary) Format() Format {
	if rs.Is3D {
		return FormatBD3D
	}
	if rs.Is4K {
		return FormatUHD
	}
	slug, _ := SlugAndIDFromURL(rs.URL)
	if slug != "" {
		return FormatFromSlug(slug)
	}
	return FormatFromTitle(rs.Title)
}

// ToIndexEntry converts a ReleaseSummary into an IndexEntry.
func (rs ReleaseSummary) ToIndexEntry() IndexEntry {
	slug, id := SlugAndIDFromURL(rs.URL)
	if rs.ID != "" {
		id = rs.ID
	}
	return IndexEntry{
		ID:      id,
		Slug:    slug,
		Title:   rs.Title,
		Format:  rs.Format(),
		Year:    rs.Year,
		Country: string(rs.Country),
	}
}

// QuickSearchResult represents an instant search match from /search/quicksearch.php.
type QuickSearchResult struct {
	ID          string
	Title       string
	URL         string
	CoverURL    string
	Country     Country
	ReleaseDate string
	Year        int
	Rating      float64
	Is3D        bool
	Is4K        bool
}

// Release is the comprehensive record extracted from a /movies/<slug>/<id>/ page.
// It unifies pipeliner's Tier-1 release fields with the extended ReleaseDetails
// specifications from bluraycom (video/audio/subtitles/discs/ratings/crew).
type Release struct {
	ID             string
	ParentMovieID  string
	ParentMovieURL string
	Title          string
	Subtitle       string // e.g., "Blu-ray 3D + Blu-ray"
	Edition        string
	URL            string
	Format         Format
	Country        string // e.g., "United States" or ISO code "us"
	CountryCode    Country
	Studio         string
	Year           int
	RuntimeMin     int
	Rated          string
	ReleaseDate    string // "YYYY-MM-DD" when parseable, else raw string
	Codec          string // e.g. "MPEG-4 MVC", "HEVC", "AVC"
	Resolution     string // e.g. "1080p", "2160p"
	AspectRatio    string // e.g. "1.78:1"
	CoverURL       string
	LargeFrontURL  string
	LargeBackURL   string
	SlipcoverURL   string

	Video     VideoSpecs
	Audio     []AudioTrack
	Subtitles []string
	Discs     DiscSpecs
	Digital   []string
	Packaging []string
	Playback  string // e.g., "2K Blu-ray: Region A (locked)"
	Price     PriceInfo

	Ratings  ReleaseRatings
	Synopsis string
	Director string
	Writers  []PersonLink
	Starring []PersonLink
	Genres   []string
}

// ReleaseDetails is an alias for Release for compatibility with bluraycom callers.
type ReleaseDetails = Release

// VideoSpecs holds parsed video technical specifications.
type VideoSpecs struct {
	Codec               string // e.g., "MPEG-4 MVC (27.50 Mbps)"
	Resolution          string // e.g., "1080p"
	AspectRatio         string // e.g., "2.40:1"
	OriginalAspectRatio string // e.g., "2.39:1"
	Is3D                bool   // True if MVC codec or 3D resolution/format
	Is4K                bool   // True if 2160p/4K
	HDR                 string // e.g., "HDR10, Dolby Vision"
	RawLines            []string
}

// AudioTrack represents a single audio track on a disc.
type AudioTrack struct {
	Language string // e.g., "English"
	Format   string // e.g., "DTS-HD Master Audio 7.1, Neo:X"
	Raw      string // e.g., "English: DTS-HD Master Audio 7.1, Neo:X"
}

// DiscSpecs holds parsed disc format and count details.
type DiscSpecs struct {
	Is3D     bool // True if "Blu-ray 3D" or "Anaglyph 3D" is present
	Is4K     bool // True if "4K Ultra HD" is present
	Count    int  // Number of discs if parsed
	Format   string
	RawLines []string
}

// PriceInfo holds pricing metadata from a release page.
type PriceInfo struct {
	ListPrice    string
	CurrentPrice string
	BuyURL       string
}

// ReleaseRatings holds movie, review, and user ratings for a release.
type ReleaseRatings struct {
	MovieScore    float64 // Out of 10
	MovieVotes    int
	ReviewVideo   float64 // Out of 5
	Review3D      float64 // Out of 5
	ReviewAudio   float64 // Out of 5
	ReviewExtras  float64 // Out of 5
	ReviewOverall float64 // Out of 5
	UserVideo     float64 // Out of 5
	User3D        float64 // Out of 5
	UserAudio     float64 // Out of 5
	UserExtras    float64 // Out of 5
	UserOverall   float64 // Out of 5
	IMDbURL       string
	RTURL         string
}

// PersonLink represents a cast or crew member link.
type PersonLink struct {
	Name string
	ID   string
	URL  string
}

// MovieDetails represents a theatrical / master movie entry on blu-ray.com.
type MovieDetails struct {
	ID               string
	Title            string
	URL              string
	Year             int
	RuntimeMins      int
	Rated            string
	Studio           string
	Country          string
	Language         string
	TechnicalDetails []string // e.g., ["2.39:1", "Native 3D, 95 mins"]
	IsNative3D       bool     // True if TechnicalDetails mentions Native 3D
	IsConverted3D    bool     // True if TechnicalDetails mentions Converted 3D
	Synopsis         string
	Director         string
	Writers          []PersonLink
	Starring         []PersonLink
	Genres           []string
	Rating           float64
}

// MovieReleases groups all editions of a movie across formats (and optionally all countries).
type MovieReleases struct {
	MovieID       string
	BluRay4K      []ReleaseSummary
	BluRay3D      []ReleaseSummary
	BluRay        []ReleaseSummary
	BluRayBundles []ReleaseSummary
	DVD           []ReleaseSummary
	Digital       []ReleaseSummary
}

// Has3D returns true if the movie has at least one 3D Blu-ray release.
func (mr *MovieReleases) Has3D() bool {
	return len(mr.BluRay3D) > 0
}

// Movie3DCheckResult summarizes whether a movie has a 3D Blu-ray release and lists all matching editions.
type Movie3DCheckResult struct {
	Query      string
	Movie      *MovieDetails
	Has3D      bool
	Releases3D []ReleaseSummary
	CheckedAt  time.Time
}

// IndexEntry is the lightweight (id, format, year) record produced by the
// calendar, search parsers, and embedded 3D catalog. Detail-only fields (codec,
// runtime, aspect) require a follow-up GetRelease call.
type IndexEntry struct {
	ID      string
	Slug    string // URL path segment, e.g. "Avatar-3D-Blu-ray"
	Title   string // human title, e.g. "Avatar 3D"
	Format  Format
	Year    int
	Country string // optional country code, e.g. "us", "de"
}

// CalendarEntry is a calendar row before it has been collapsed into either an
// IndexEntry or an entry.Entry. It carries the calendar-only fields (studio,
// releasedate, edition, casing) that are not present in basic search-result rows.
type CalendarEntry struct {
	IndexEntry
	Edition     string
	Studio      string
	ReleaseDate string // "YYYY-MM-DD"
	Casing      string // e.g. "SteelBook"
}

// Is3DRelease reports whether at least one entry in entries has Format=BD3D.
// Used by both plugins to compute bluray_3d_release.
func Is3DRelease(entries []IndexEntry) bool {
	for _, e := range entries {
		if e.Format == FormatBD3D {
			return true
		}
	}
	return false
}
