package mediaserver

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Connect builds a Client for a plex/jellyfin backend from the three config
// keys every caller spells the same way. Plex with neither url nor token is
// account mode: it spans every owned server using the Settings-tab sign-in,
// and the token is read per call so signing in after daemon start takes
// effect without a restart.
//
// It exists so the plugins that read a media server agree on what those keys
// mean. They had better, since a disagreement would show up as one plugin
// seeing a library the other cannot.
func Connect(backend, url, token string, bucket tokenBucket) (Client, error) {
	switch {
	case backend == "plex" && url == "" && token == "":
		if bucket == nil {
			return nil, fmt.Errorf("plex account mode requires the store")
		}
		return NewPlexAccount(func() string { return PlexAccountToken(bucket) }), nil
	case url == "" || token == "":
		return nil, fmt.Errorf("backend %q requires 'url' and 'token'", backend)
	default:
		return New(backend, url, token)
	}
}

// Sections matches server library names against an include/exclude filter.
// An empty filter wants everything.
type Sections struct {
	include map[string]bool
	exclude map[string]bool
}

// NewSections builds a filter from the sections/exclude_sections config
// values. Names are matched case-insensitively, since they are typed by hand
// into a config and displayed by the server with its own capitalisation.
func NewSections(include, exclude []string) Sections {
	s := Sections{}
	if len(include) > 0 {
		s.include = make(map[string]bool, len(include))
		for _, n := range include {
			s.include[strings.ToLower(strings.TrimSpace(n))] = true
		}
	}
	if len(exclude) > 0 {
		s.exclude = make(map[string]bool, len(exclude))
		for _, n := range exclude {
			s.exclude[strings.ToLower(strings.TrimSpace(n))] = true
		}
	}
	return s
}

// Want reports whether a library by this name should be read. include wins
// when both are set, matching the library filter's long-standing behaviour.
func (s Sections) Want(name string) bool {
	n := strings.ToLower(name)
	if len(s.include) > 0 {
		return s.include[n]
	}
	return !s.exclude[n]
}

// Filtered reports whether any filter is configured, which a caller needs in
// order to warn when a server reports no library names and the filter
// therefore cannot be honoured.
func (s Sections) Filtered() bool {
	return len(s.include) > 0 || len(s.exclude) > 0
}

// OwnedEpisodes is which episodes of which shows a media server holds. It
// answers the two questions a gap scan asks: whether one episode is present,
// and which season of a show is the earliest with anything in it.
//
// Shows are keyed by a caller-supplied normalisation of the title, because
// the server's spelling and a release name's spelling rarely match exactly.
type OwnedEpisodes struct {
	// shows maps normalised show -> season -> set of episode numbers.
	shows map[string]map[int]map[int]bool
	// titles maps normalised show -> the title the server reports, kept
	// because a normalised key is not something to put in a search query.
	titles map[string]string
	// byTVDB maps a TheTVDB series id -> the normalised key above, for the
	// shows whose server publishes one. See Resolve for why it exists.
	byTVDB map[string]string
	// tvdbIDs is the reverse, so a caller listing the library can pass the id
	// on to whatever consumes its output.
	tvdbIDs map[string]string
}

// Resolve maps a show to the key this index is keyed by, preferring the
// TheTVDB id the server published over the title, and falling back to the
// normalised name when there is no id or no show carrying it.
//
// The id matters because a title is not stable identity. TheTVDB renames
// series, and a server may disambiguate a remake with a year the provider does
// not use -- a library holding "Brothers (2026)" against a provider calling it
// "Brothers" normalises to "brothers 2026" and "brothers", so a name lookup
// misses a show that is plainly present. Nothing says so in the logs either:
// the show simply yields no season floor and contributes nothing.
//
// The returned key is safe to pass to Has, HasAnyInSeason and
// FirstSeasonWithAny whether or not it matched, since an absent key answers
// "not owned" exactly as before.
func (o *OwnedEpisodes) Resolve(tvdbID, name string) string {
	if o == nil {
		return name
	}
	if tvdbID != "" {
		if key, ok := o.byTVDB[tvdbID]; ok {
			return key
		}
	}
	return name
}

// TVDBID returns the TheTVDB series id the server published for a show, given
// its index key, or "" when the server exposed none.
func (o *OwnedEpisodes) TVDBID(show string) string {
	if o == nil {
		return ""
	}
	return o.tvdbIDs[show]
}

// Has reports whether the server holds that episode.
func (o *OwnedEpisodes) Has(show string, season, episode int) bool {
	if o == nil {
		return false
	}
	return o.shows[show][season][episode]
}

// HasAnyInSeason reports whether the server holds at least one episode of
// that season.
func (o *OwnedEpisodes) HasAnyInSeason(show string, season int) bool {
	if o == nil {
		return false
	}
	return len(o.shows[show][season]) > 0
}

// FirstSeasonWithAny returns the lowest season number holding at least one
// episode of the show, and whether the show is present at all. Season 0
// (specials) counts only when includeSpecials is set: a stray special should
// not make every numbered season look like a backfill target.
func (o *OwnedEpisodes) FirstSeasonWithAny(show string, includeSpecials bool) (int, bool) {
	if o == nil {
		return 0, false
	}
	seasons := o.shows[show]
	if len(seasons) == 0 {
		return 0, false
	}
	first, found := 0, false
	for season, eps := range seasons {
		if len(eps) == 0 {
			continue
		}
		if season == 0 && !includeSpecials {
			continue
		}
		if !found || season < first {
			first, found = season, true
		}
	}
	return first, found
}

// Shows returns the normalised show keys present, sorted. For logging and
// tests; the hot paths use the lookups above.
func (o *OwnedEpisodes) Shows() []string {
	if o == nil {
		return nil
	}
	out := make([]string, 0, len(o.shows))
	for s := range o.shows {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Count returns the number of episodes indexed, for logging.
func (o *OwnedEpisodes) Count() int {
	if o == nil {
		return 0
	}
	var n int
	for _, seasons := range o.shows {
		for _, eps := range seasons {
			n += len(eps)
		}
	}
	return n
}

// BuildOwnedEpisodes lists the server's items and indexes the episodes among
// them. normalize maps a server-reported show title to the key the caller
// will look it up by.
//
// An error from the server is returned rather than swallowed: an empty index
// is indistinguishable from an empty library, and a caller that treated an
// unreachable server as "you own nothing" would propose re-downloading
// everything.
func BuildOwnedEpisodes(ctx context.Context, c Client, sections Sections, normalize func(string) string) (*OwnedEpisodes, error) {
	items, err := c.ListItems(ctx)
	if err != nil {
		return nil, err
	}
	o := &OwnedEpisodes{
		shows:   map[string]map[int]map[int]bool{},
		titles:  map[string]string{},
		byTVDB:  map[string]string{},
		tvdbIDs: map[string]string{},
	}
	for _, it := range items {
		if it.Type != "episode" || it.Show == "" {
			continue
		}
		if it.Section != "" && !sections.Want(it.Section) {
			continue
		}
		key := normalize(it.Show)
		if key == "" {
			continue
		}
		if _, ok := o.titles[key]; !ok {
			o.titles[key] = it.Show
		}
		if it.ShowTVDBID != "" {
			// First writer wins, matching titles above. Two library shows
			// claiming one id (a duplicate folder, say) would otherwise make
			// the mapping depend on listing order.
			if _, ok := o.byTVDB[it.ShowTVDBID]; !ok {
				o.byTVDB[it.ShowTVDBID] = key
			}
			if _, ok := o.tvdbIDs[key]; !ok {
				o.tvdbIDs[key] = it.ShowTVDBID
			}
		}
		seasons := o.shows[key]
		if seasons == nil {
			seasons = map[int]map[int]bool{}
			o.shows[key] = seasons
		}
		eps := seasons[it.Season]
		if eps == nil {
			eps = map[int]bool{}
			seasons[it.Season] = eps
		}
		eps[it.Episode] = true
	}
	return o, nil
}

// Title returns the server's spelling of a show, given its normalised key.
// Falls back to the key when the show is not present, so a caller always has
// something printable.
func (o *OwnedEpisodes) Title(show string) string {
	if o == nil {
		return show
	}
	if t := o.titles[show]; t != "" {
		return t
	}
	return show
}

// EpisodeCount returns how many episodes of a show are held.
func (o *OwnedEpisodes) EpisodeCount(show string) int {
	if o == nil {
		return 0
	}
	var n int
	for _, eps := range o.shows[show] {
		n += len(eps)
	}
	return n
}

// SeasonCount returns how many distinct seasons of a show hold at least one
// episode.
func (o *OwnedEpisodes) SeasonCount(show string) int {
	if o == nil {
		return 0
	}
	var n int
	for _, eps := range o.shows[show] {
		if len(eps) > 0 {
			n++
		}
	}
	return n
}
