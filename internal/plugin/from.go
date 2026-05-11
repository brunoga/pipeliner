package plugin

import (
	"context"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
)

// CacheKeyer may be implemented by source plugins used as from-plugin title
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

func (p *loggedSourcePlugin) Name() string  { return p.inner.Name() }
func (p *loggedSourcePlugin) Phase() Phase  { return p.inner.Phase() }
func (p *loggedSourcePlugin) CacheKey() string { return sourceKey(p.inner) }

func (p *loggedSourcePlugin) Generate(ctx context.Context, tc *TaskContext) ([]*entry.Entry, error) {
	tc.Logger.Info("from plugin started", "plugin", p.inner.Name())
	start := time.Now()
	entries, err := p.inner.Generate(ctx, tc)
	dur := time.Since(start).Round(time.Millisecond)
	if err != nil {
		tc.Logger.Warn("from plugin failed", "plugin", p.inner.Name(), "err", err, "duration", dur)
		return nil, err
	}
	tc.Logger.Info("from plugin done", "plugin", p.inner.Name(), "count", len(entries), "duration", dur)
	return entries, nil
}

// ResolveDynamicList fetches normalized titles from source plugins, merges
// them with static titles, and manages per-source caching. Each source is
// cached independently so sources can be invalidated and re-fetched individually.
//
// Returns the merged list: static titles first, then dynamic titles.
func ResolveDynamicList(
	ctx context.Context,
	tc *TaskContext,
	froms []SourcePlugin,
	static []string,
	cacheGet func(source string) ([]string, bool),
	cacheSet func(source string, titles []string),
	normalise func(string) string,
) []string {
	if len(froms) == 0 {
		return static
	}
	innerTC := &TaskContext{Name: tc.Name, Logger: tc.Logger}
	var dynamic []string
	for _, src := range froms {
		key := sourceKey(src)
		if cached, ok := cacheGet(key); ok {
			tc.Logger.Debug("from: loaded list from cache", "plugin", src.Name(), "key", key, "count", len(cached))
			dynamic = append(dynamic, cached...)
			continue
		}
		fromEntries, err := src.Generate(ctx, innerTC)
		if err != nil {
			continue // error already logged by loggedSourcePlugin
		}
		var titles []string
		for _, e := range fromEntries {
			if e.Title != "" {
				titles = append(titles, normalise(e.Title))
			}
		}
		if len(titles) > 0 {
			cacheSet(key, titles)
		}
		dynamic = append(dynamic, titles...)
	}
	return append(static, dynamic...)
}
