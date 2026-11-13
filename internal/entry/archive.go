package entry

import (
	"path"
	"regexp"
	"strings"
)

// reArchive matches the files of an archive set: .rar and its old-style
// volumes (.r00, .r01, ...), .zip, .7z and split 7z volumes (.7z.001).
var reArchive = regexp.MustCompile(`(?i)\.(rar|r\d{2,3}|zip|7z|7z\.\d{3})$`)

// videoExtensions are the containers a media server plays as they are.
var videoExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true,
	".ts": true, ".m2ts": true, ".wmv": true, ".webm": true, ".mpg": true, ".mpeg": true,
}

// isSample reports whether a file is a release's sample clip. A scene RAR pack
// ships one ("Sample/show.s01-sample.mkv") beside the archives, and counting it
// as the video would send the archives straight to the library unextracted.
func isSample(f string) bool {
	return strings.Contains(strings.ToLower(f), "sample")
}

// ArchivedPayload reports whether a torrent with these files carries its
// video inside archives: at least one archive file and no video file. A torrent
// holding videos beside a "subs.rar" or an "extras.zip" is not archived -- its
// video plays as it is -- and one with neither is not something an extractor
// can help with.
func ArchivedPayload(files []string) bool {
	archive := false
	for _, f := range files {
		name := strings.ToLower(path.Base(f))
		if videoExtensions[path.Ext(name)] && !isSample(f) {
			return false
		}
		if reArchive.MatchString(name) {
			archive = true
		}
	}
	return archive
}
