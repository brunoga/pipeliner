package tvdb

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
)

// CachedEpisodes is the JSON shape shared by the episode caches of the TVDB
// metadata plugins (lowercase "name"/"episodes") so the database tab labels
// rows the same way regardless of which plugin wrote them.
type CachedEpisodes struct {
	Name     string    `json:"name"`
	Episodes []Episode `json:"episodes"`
}

// Resolver bundles the two lookups TVDB-backed plugins need — series
// resolution (by TVDB id when known, else name search) and episode lists —
// with persistent TTL caches, so plugins share one implementation instead of
// keeping private copies of the same logic.
type Resolver struct {
	// Client is exported so tests can point BaseURL at an httptest server.
	Client       *Client
	searchCache  *cache.Cache[[]Series]
	episodeCache *cache.Cache[CachedEpisodes]
}

// NewResolver wraps client with TTL caches backed by the given buckets and
// preloads both caches.
func NewResolver(client *Client, ttl time.Duration, searchBucket, episodeBucket cache.Bucket) *Resolver {
	r := &Resolver{
		Client:       client,
		searchCache:  cache.NewPersistent[[]Series](ttl, searchBucket),
		episodeCache: cache.NewPersistent[CachedEpisodes](ttl, episodeBucket),
	}
	r.searchCache.Preload()
	r.episodeCache.Preload()
	return r
}

// ResolveSeries finds the series record: by tvdbID when it is a positive
// integer string, otherwise by name search. Both paths are cached. Returns
// (nil, nil) when the search succeeded but found no match — that outcome is
// cached too, so a missing show does not re-hit the API every run.
func (r *Resolver) ResolveSeries(ctx context.Context, tvdbID, name string) (*Series, error) {
	if tvdbID != "" {
		if id, err := strconv.Atoi(tvdbID); err == nil && id > 0 {
			cacheKey := "id:" + tvdbID
			if hit, ok := r.searchCache.Get(cacheKey); ok && len(hit) > 0 {
				return &hit[0], nil
			}
			s, err := r.Client.GetSeriesByID(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("series by id %s: %w", tvdbID, err)
			}
			// GetSeriesByID responses omit the search alias "tvdb_id" key, so
			// carry the ID forward explicitly before caching.
			if s.ID == "" {
				s.ID = tvdbID
			}
			r.searchCache.Set(cacheKey, []Series{*s})
			return s, nil
		}
	}

	if hit, ok := r.searchCache.Get(name); ok {
		if len(hit) == 0 {
			return nil, nil
		}
		return &hit[0], nil
	}
	results, err := r.Client.SearchSeries(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("search %q: %w", name, err)
	}
	r.searchCache.Set(name, results)
	if len(results) == 0 {
		return nil, nil
	}
	return &results[0], nil
}

// Episodes returns the official episode list for the series, cached under
// its TVDB id. name is stored alongside the episodes for display purposes.
func (r *Resolver) Episodes(ctx context.Context, id, name string) ([]Episode, error) {
	if id == "" {
		return nil, errors.New("series has no tvdb id")
	}
	if hit, ok := r.episodeCache.Get(id); ok {
		return hit.Episodes, nil
	}
	eps, err := r.Client.GetEpisodes(ctx, id)
	if err != nil {
		return nil, err
	}
	r.episodeCache.Set(id, CachedEpisodes{Name: name, Episodes: eps})
	return eps, nil
}

// EpisodeAired reports whether ep counts as an already-aired regular episode:
// season 0 specials are excluded unless includeSpecials, malformed
// season/episode numbers never count, and the air date must parse and be
// strictly before now (unaired or unscheduled episodes don't count).
func EpisodeAired(ep *Episode, now time.Time, includeSpecials bool) bool {
	if ep.SeasonNumber == 0 && !includeSpecials {
		return false
	}
	if ep.SeasonNumber < 0 || ep.EpisodeNumber <= 0 {
		return false
	}
	airDate := ParseDate(ep.AirDate)
	return !airDate.IsZero() && airDate.Before(now)
}
