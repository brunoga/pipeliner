package plugin

import (
	"context"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
)

// loggedFromPlugin wraps an InputPlugin and emits info-level start/done log
// lines with timing around every Run call. Created by MakeFromPlugin.
type loggedFromPlugin struct {
	inner InputPlugin
}

func (p *loggedFromPlugin) Name() string        { return p.inner.Name() }
func (p *loggedFromPlugin) Phase() Phase        { return p.inner.Phase() }

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
// with static titles, and manages caching via the caller-supplied get/set
// functions. Cache hits are logged at debug; live fetches are logged at info
// (via the logging wrapper in each from-plugin).
//
// Returns the merged list: static titles first, then dynamic titles.
func ResolveDynamicList(
	ctx context.Context,
	tc *TaskContext,
	froms []InputPlugin,
	static []string,
	cacheGet func() ([]string, bool),
	cacheSet func([]string),
	normalise func(string) string,
) []string {
	if len(froms) == 0 {
		return static
	}
	if dynamic, ok := cacheGet(); ok {
		tc.Logger.Debug("from: loaded list from cache", "count", len(dynamic))
		return append(static, dynamic...)
	}
	var dynamic []string
	innerTC := &TaskContext{Name: tc.Name, Logger: tc.Logger}
	for _, inp := range froms {
		fromEntries, err := inp.Run(ctx, innerTC)
		if err != nil {
			// error already logged by loggedFromPlugin
			continue
		}
		for _, e := range fromEntries {
			if e.Title != "" {
				dynamic = append(dynamic, normalise(e.Title))
			}
		}
	}
	cacheSet(dynamic)
	return append(static, dynamic...)
}
