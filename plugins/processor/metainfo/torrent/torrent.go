// Package torrent implements a metainfo plugin that parses .torrent files and
// annotates entries with their metadata (name, info hash, size, tracker, etc.).
//
// The plugin reads the torrent from the entry's "location" field (a local file
// path set by the filesystem input plugin) when present, or by fetching the URL
// when any of the following is true:
//   - the URL ends in ".torrent"
//   - rss_enclosure_type is "application/x-bittorrent" (RSS torrent feeds)
//   - torrent_info_hash is already set (e.g. by jackett_input / jackett) —
//     these entries have a Jackett proxy URL that serves the .torrent bytes
//
// Config keys:
//
//	fetch_timeout - HTTP timeout for downloading .torrent files (default: 30s)
//
// Fields set on the entry:
//
//	torrent_name          - torrent display name
//	torrent_info_hash     - hex SHA-1 of the info dict
//	torrent_size          - total content size in bytes
//	torrent_file_count    - number of files (1 for single-file torrents)
//	torrent_files         - []string of relative file paths within the torrent
//	torrent_announce      - primary tracker URL
//	torrent_comment       - torrent comment field
//	torrent_created_by    - torrent creation software
//	torrent_creation_date - creation timestamp (Unix seconds)
//	torrent_private       - true if private flag is set
package torrent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/bencode"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "metainfo_torrent",
		PluginPhase: plugin.PhaseMetainfo,
		Role:        plugin.RoleProcessor,
		Description: "Annotates entries from .torrent files with name, info hash, size, and tracker metadata",
		Produces: []string{
			entry.FieldTitle,
			entry.FieldTorrentInfoHash,
			entry.FieldTorrentFileSize,
			entry.FieldTorrentFileCount,
			entry.FieldTorrentFiles,
			entry.FieldTorrentAnnounce,
			entry.FieldTorrentAnnounceList,
			entry.FieldTorrentCreatedBy,
			entry.FieldTorrentCreationDate,
			entry.FieldTorrentPrivate,
		},
		Factory:  newPlugin,
		Validate: validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.OptDuration(cfg, "fetch_timeout", "metainfo_torrent"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "metainfo_torrent", "fetch_timeout")...)
	return errs
}

type torrentPlugin struct {
	client *http.Client
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	fetchTimeout := 30 * time.Second
	if v, ok := cfg["fetch_timeout"]; ok {
		s, _ := v.(string)
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, err
		}
		fetchTimeout = d
	}
	return &torrentPlugin{
		client: &http.Client{Timeout: fetchTimeout},
	}, nil
}

func (p *torrentPlugin) Name() string        { return "metainfo_torrent" }
func (p *torrentPlugin) Phase() plugin.Phase { return plugin.PhaseMetainfo }

func (p *torrentPlugin) annotate(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	log := tc.Logger
	loc := e.GetString(entry.FieldFileLocation)
	log.Debug("metainfo_torrent: received entry",
		"entry", e.URL,
		"has_location", loc != "",
	)

	data, err := p.readTorrent(ctx, log, e)
	if err != nil {
		log.Error("metainfo_torrent: failed to read torrent", "entry", e.URL, "err", err)
		return fmt.Errorf("metainfo_torrent: %w", err)
	}
	if data == nil {
		log.Debug("metainfo_torrent: skipping entry — not recognised as a torrent",
			"entry", e.URL,
			"location", loc,
		)
		return nil
	}

	log.Debug("metainfo_torrent: decoding torrent", "entry", e.URL, "bytes", len(data))
	ti, err := bencode.DecodeTorrent(data)
	if err != nil {
		log.Error("metainfo_torrent: failed to decode torrent", "entry", e.URL, "err", err)
		return fmt.Errorf("metainfo_torrent: decode: %w", err)
	}

	var creationTime time.Time
	if ti.CreationDate != 0 {
		creationTime = time.Unix(ti.CreationDate, 0)
	}
	e.SetTorrentInfo(entry.TorrentInfo{
		GenericInfo:  entry.GenericInfo{Title: ti.Name, Description: ti.Comment},
		FileSize:     ti.TotalSize,
		FileCount:    ti.FileCount,
		Files:        ti.Files,
		InfoHash:     ti.InfoHash,
		Announce:     ti.Announce,
		AnnounceList: ti.AnnounceList,
		CreatedBy:    ti.CreatedBy,
		CreationDate: creationTime,
		Private:      ti.IsPrivate,
	})

	log.Debug("metainfo_torrent: annotated",
		"entry", e.URL,
		"name", ti.Name,
		"info_hash", ti.InfoHash,
		"size", ti.TotalSize,
		"files", ti.FileCount,
		"announce", ti.Announce,
		"trackers", len(ti.AnnounceList),
		"private", ti.IsPrivate,
		"created_by", ti.CreatedBy,
	)
	return nil
}

// torrentURLReason returns a short reason string if the entry's URL should be
func (p *torrentPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		if err := p.annotate(ctx, tc, e); err != nil {
			tc.Logger.Warn("metainfo_torrent error", "entry", e.Title, "err", err)
		}
	}
	return entries, nil
}

// torrentURLReason returns the reason a URL should be
// fetched as a torrent, or "" if the entry should be skipped.
// It checks torrent_link_type first (set by sources such as Jackett that know
// the link type without an HTTP fetch), then falls back to URL inspection.
func torrentURLReason(e *entry.Entry) string {
	switch e.GetString(entry.FieldTorrentLinkType) {
	case "torrent":
		return "torrent_link_type=torrent"
	case "magnet":
		return "" // magnet — handled by metainfo_magnet
	}
	// Fallback: inspect the URL directly.
	if strings.HasSuffix(strings.ToLower(e.URL), ".torrent") {
		return ".torrent URL"
	}
	if et := e.GetString(entry.FieldRSSEnclosureType); et == "application/x-bittorrent" || et == "application/x-torrent" {
		return "rss_enclosure_type=" + et
	}
	return ""
}

// readTorrent returns raw torrent bytes from a local file or by downloading the
// URL. Returns (nil, nil) if this entry does not appear to be a .torrent.
func (p *torrentPlugin) readTorrent(ctx context.Context, log interface {
	Debug(string, ...any)
	Error(string, ...any)
}, e *entry.Entry) ([]byte, error) {
	// Prefer local file (set by filesystem input plugin).
	if loc := e.GetString(entry.FieldFileLocation); loc != "" && strings.HasSuffix(strings.ToLower(loc), ".torrent") {
		log.Debug("metainfo_torrent: reading from local file", "path", loc)
		data, err := os.ReadFile(loc)
		if err != nil {
			return nil, err
		}
		log.Debug("metainfo_torrent: local file read", "path", loc, "bytes", len(data))
		return data, nil
	}
	// Fall back to URL — recognise the entry as a torrent download when:
	//   (a) URL ends in ".torrent"
	//   (b) rss_enclosure_type signals a torrent file (RSS feeds)
	//   (c) torrent_info_hash is already set by an upstream plugin such as
	//       jackett_input, which returns Jackett proxy URLs that serve the
	//       .torrent bytes even though the URL has no ".torrent" suffix
	reason := torrentURLReason(e)
	if reason == "" {
		return nil, nil
	}
	log.Debug("metainfo_torrent: fetching from URL", "url", e.URL, "reason", reason)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Error("metainfo_torrent: unexpected HTTP status", "url", e.URL, "status", resp.StatusCode)
		return nil, fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, e.URL)
	}
	log.Debug("metainfo_torrent: HTTP response received",
		"url", e.URL,
		"status", resp.StatusCode,
		"content_length", resp.ContentLength,
	)
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	log.Debug("metainfo_torrent: download complete", "url", e.URL, "bytes", len(data))
	return data, nil
}
