package plugin

import (
	"context"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
)

// CacheKeyer may be implemented by from-plugins whose identity depends on
// configuration beyond the registered plugin name — for example, a plugin
// that can be configured with different list parameters. ResolveDynamicList
// uses CacheKey() as the per-source cache key when the interface is present,
// falling back to Name() otherwise.
type CacheKeyer interface {
	CacheKey() string
}

// sourceKey returns the cache key for a from-plugin: CacheKey() if the plugin
// implements CacheKeyer, otherwise Name().
func sourceKey(inp InputPlugin) string {
	if ck, ok := inp.(CacheKeyer); ok {
		return ck.CacheKey()
	}
	return inp.Name()
}

// loggedFromPlugin wraps an InputPlugin and emits info-level start/done log
// lines with timing around every Run call. Created by MakeFromPlugin.
type loggedFromPlugin struct {
	inner InputPlugin
}

func (p *loggedFromPlugin) Name() string        { return p.inner.Name() }
func (p *loggedFromPlugin) Phase() Phase        { return p.inner.Phase() }
func (p *loggedFromPlugin) CacheKey() string    { return sourceKey(p.inner) }

func (p *loggedFromPlugin) Run(ctx context.Context, tc *TaskContext) ([]*entry.Entry, error) {
	tc.Logger.Info("from plugin started", "plugin", p.inner.Name())
	start := time.Now()
	entries, err := p.inner.Run(ctx, tc)
	dur := time.Since(start).Round(time.Millisecond)
	if err != nil {
		tc.Logger.Warn("from plugin failed", "plugin", p.inner.Name(), "err", err, "duration", dur)
		return nil, err
	}
	tc.Logger.Info("from plugin done", "plugin", p.inner.Name(), "count", len(entries), "duration", dur)
	return entries, nil
}

// ResolveDynamicList fetches normalized titles from from-plugins, merges them
// with static titles, and manages per-source caching via the caller-supplied
// get/set functions. Each from-plugin is cached independently under its own
// name so sources can be invalidated and re-fetched individually.
//
// Cache hits are logged at debug; live fetches are logged at info via the
// logging wrapper in each from-plugin.
//
// Returns the merged list: static titles first, then dynamic titles.
func ResolveDynamicList(
	ctx context.Context,
	tc *TaskContext,
	froms []InputPlugin,
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
	for _, inp := range froms {
		key := sourceKey(inp)
		if cached, ok := cacheGet(key); ok {
			tc.Logger.Debug("from: loaded list from cache", "plugin", inp.Name(), "key", key, "count", len(cached))
			dynamic = append(dynamic, cached...)
			continue
		}
		fromEntries, err := inp.Run(ctx, innerTC)
		if err != nil {
			// error already logged by loggedFromPlugin
			continue
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
