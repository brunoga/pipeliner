package plugin

import (
	"context"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
)

// CacheKeyer may be implemented by source plugins used as list sub-plugin title
// sources. ResolveDynamicList uses CacheKey() as the per-source cache key when
// the interface is present, falling back to Name() otherwise.
type CacheKeyer interface {
	CacheKey() string
}

func sourceKey(src SourcePlugin) string {
	if ck, ok := src.(CacheKeyer); ok {
		return ck.CacheKey()
	}
	return src.Name()
}

// loggedSourcePlugin wraps a SourcePlugin and emits info-level start/done
// log lines with timing around every Generate call.
type loggedSourcePlugin struct {
	inner SourcePlugin
}

func (p *loggedSourcePlugin) Name() string     { return p.inner.Name() }
func (p *loggedSourcePlugin) CacheKey() string { return sourceKey(p.inner) }

func (p *loggedSourcePlugin) Generate(ctx context.Context, tc *TaskContext) ([]*entry.Entry, error) {
	tc.Logger.Info("list plugin started", "plugin", p.inner.Name())
	start := time.Now()
	entries, err := p.inner.Generate(ctx, tc)
	dur := time.Since(start).Round(time.Millisecond)
	if err != nil {
		tc.Logger.Warn("list plugin failed", "plugin", p.inner.Name(), "err", err, "duration", dur)
		return nil, err
	}
	tc.Logger.Info("list plugin done", "plugin", p.inner.Name(), "count", len(entries), "duration", dur)
	return entries, nil
}

// ResolveDynamicList fetches title entries from source plugins, merges them
// with static entries, and manages per-source caching. Each source is cached
// independently so sources can be invalidated and re-fetched individually.
// Year information from entry.ReleaseYear is preserved in each TitleEntry so
// callers can do year-aware matching.
//
// Returns the merged list: static entries first, then dynamic entries.
func ResolveDynamicList(
	ctx context.Context,
	tc *TaskContext,
	froms []SourcePlugin,
	static []match.TitleEntry,
	cacheGet func(source string) ([]match.TitleEntry, bool),
	cacheSet func(source string, entries []match.TitleEntry),
) []match.TitleEntry {
	if len(froms) == 0 {
		return static
	}
	innerTC := &TaskContext{Name: tc.Name, Logger: tc.Logger}
	var dynamic []match.TitleEntry
	for _, src := range froms {
		key := sourceKey(src)
		if cached, ok := cacheGet(key); ok {
			tc.Logger.Debug("list: loaded from cache", "plugin", src.Name(), "key", key, "count", len(cached))
			dynamic = append(dynamic, cached...)
			continue
		}
		fromEntries, err := src.Generate(ctx, innerTC)
		if err != nil {
			continue // error already logged by loggedSourcePlugin
		}
		var titleEntries []match.TitleEntry
		for _, e := range fromEntries {
			if e.Title != "" {
				titleEntries = append(titleEntries, match.NewTitleEntry(e.Title, entry.ReleaseYear(e)))
			}
		}
		if len(titleEntries) > 0 {
			cacheSet(key, titleEntries)
		}
		dynamic = append(dynamic, titleEntries...)
	}
	return append(static, dynamic...)
}
