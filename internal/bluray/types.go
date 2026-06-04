// Package bluray is a Blu-ray.com HTML scraper used by the bluray_releases
// source plugin and the metainfo_bluray processor plugin.
//
// The package exposes a small typed client over three endpoints:
//
//   - GET /movies/<slug>/<id>/                 release detail
//   - GET /movies/releasedates.php?year=Y&month=M release calendar
//   - GET /search/?quicksearch=1&…             title search
//
// The release calendar embeds the entire month's release list as a JS data
// literal that is much more stable to parse than the DOM. The detail page is
// scraped via string-and-regex extraction (no goquery), matching the
// dependency-free convention of plugins/source/html.
package bluray

// Format identifies which physical media a Blu-ray.com release record describes.
type Format string

const (
	FormatBD   Format = "BD"   // standard Blu-ray Disc
	FormatUHD  Format = "UHD"  // 4K Ultra HD Blu-ray
	FormatBD3D Format = "BD3D" // 3D Blu-ray (frame-packed MVC)
	FormatDVD  Format = "DVD"  // DVD-Video
)

// Release is the Tier 1 record extracted from a /movies/<slug>/<id>/ page.
// Empty strings/zero ints indicate the field was not present on the page.
type Release struct {
	ID          string
	Title       string
	Edition     string
	URL         string
	Format      Format
	Country     string
	Studio      string
	Year        int
	RuntimeMin  int
	ReleaseDate string // "YYYY-MM-DD"
	Codec       string // e.g. "MPEG-4 MVC", "HEVC", "AVC"
	Resolution  string // e.g. "1080p", "2160p"
	AspectRatio string // e.g. "1.78:1"
}

// IndexEntry is the lightweight (id, format, year) record produced by the
// calendar and search parsers. Detail-only fields (codec, runtime, aspect)
// require a follow-up GetRelease call.
type IndexEntry struct {
	ID     string
	Slug   string // URL path segment, e.g. "Avatar-3D-Blu-ray"
	Title  string // human title, e.g. "Avatar 3D"
	Format Format
	Year   int
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
