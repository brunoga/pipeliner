// Package magnet provides a metainfo plugin that annotates entries with
// metadata extracted from magnet URIs, optionally resolving full torrent
// metadata via DHT using github.com/anacrolix/torrent.
//
// Fields set on the entry from the magnet URI itself:
//
//	torrent_info_hash     - hex SHA-1 info hash (40 chars)
//	torrent_announce      - first tracker announce URL, if any
//	torrent_announce_list - []string of all tracker announce URLs
//	torrent_display_name  - human-readable name from dn= parameter, if present
//
// Fields set after DHT resolution (when the client successfully contacts peers):
//
//	torrent_name       - name from the info dict
//	torrent_size       - total size in bytes (int64)
//	torrent_file_count - number of files (int)
//	torrent_files      - []string of file paths relative to the torrent root
package magnet

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"

	"github.com/brunoga/pipeliner/internal/entry"
	imagnet "github.com/brunoga/pipeliner/internal/magnet"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "metainfo_magnet",
		Description: "annotate entries whose URL is a magnet link with info hash, tracker and DHT metadata",
		PluginPhase: plugin.PhaseMetainfo,
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.OptDuration(cfg, "resolve_timeout", "metainfo_magnet"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "metainfo_magnet", "resolve_timeout")...)
	return errs
}

type magnetPlugin struct {
	client         *torrent.Client
	resolveTimeout time.Duration
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	resolveTimeout := 30 * time.Second
	if v, ok := cfg["resolve_timeout"]; ok {
		s, _ := v.(string)
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, err
		}
		resolveTimeout = d
	}

	tcfg := torrent.NewDefaultClientConfig()
	tcfg.NoUpload = true
	tcfg.Seed = false
	tcfg.ListenPort = 0
	tcfg.DataDir = os.TempDir()

	cl, err := torrent.NewClient(tcfg)
	if err != nil {
		return nil, err
	}

	return &magnetPlugin{
		client:         cl,
		resolveTimeout: resolveTimeout,
	}, nil
}

func (p *magnetPlugin) Name() string        { return "metainfo_magnet" }
func (p *magnetPlugin) Phase() plugin.Phase { return plugin.PhaseMetainfo }

// Annotate handles the single-entry path (used by tests and external callers).
// It sets URI-derived fields only; DHT resolution requires AnnotateBatch.
func (p *magnetPlugin) Annotate(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	return annotateFromURI(e)
}

// AnnotateBatch implements BatchMetainfoPlugin. It first annotates all entries
// from their magnet URIs, then fires DHT resolution for all of them in
// parallel, waiting up to resolveTimeout for each.
func (p *magnetPlugin) AnnotateBatch(ctx context.Context, _ *plugin.TaskContext, entries []*entry.Entry) error {
	type work struct {
		t *torrent.Torrent
		e *entry.Entry
	}

	var jobs []work
	for _, e := range entries {
		if !strings.HasPrefix(e.URL, "magnet:") {
			continue
		}
		annotateFromURI(e) //nolint:errcheck
		t, err := p.client.AddMagnet(e.URL)
		if err != nil {
			continue
		}
		jobs = append(jobs, work{t: t, e: e})
	}

	if len(jobs) == 0 {
		return nil
	}

	resolveCtx, cancel := context.WithTimeout(ctx, p.resolveTimeout)
	defer cancel()

	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(t *torrent.Torrent, e *entry.Entry) {
			defer wg.Done()
			defer t.Drop()
			select {
			case <-t.GotInfo():
				applyInfo(t, e)
			case <-resolveCtx.Done():
				// timeout or parent cancelled — entry keeps URI-derived fields only
			}
		}(j.t, j.e)
	}
	wg.Wait()
	return nil
}

// annotateFromURI parses the magnet URI and sets torrent_info_hash,
// torrent_announce, torrent_announce_list, and torrent_display_name.
func annotateFromURI(e *entry.Entry) error {
	if !strings.HasPrefix(e.URL, "magnet:") {
		return nil
	}
	m, err := imagnet.Parse(e.URL)
	if err != nil {
		return nil // silently skip malformed magnet URIs
	}

	e.Set("torrent_info_hash", m.InfoHash)
	e.Set("torrent_announce_list", m.Trackers)
	if len(m.Trackers) > 0 {
		e.Set("torrent_announce", m.Trackers[0])
	}
	if m.DisplayName != "" {
		e.Set("torrent_display_name", m.DisplayName)
	}
	return nil
}

// applyInfo copies metadata from the resolved torrent info into the entry.
func applyInfo(t *torrent.Torrent, e *entry.Entry) {
	e.Set("torrent_name", t.Name())
	e.Set("torrent_size", t.Length())

	files := t.Files()
	e.Set("torrent_file_count", len(files))
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path()
	}
	e.Set("torrent_files", paths)
}
