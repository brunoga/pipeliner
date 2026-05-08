// Package torrent implements a metainfo plugin that parses .torrent files and
// annotates entries with their metadata (name, info hash, size, tracker, etc.).
//
// The plugin reads the torrent from the entry's "location" field (a local file
// path set by the filesystem input plugin) when present, or by downloading the
// URL if it ends in ".torrent".
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
		Description: "Annotates entries from .torrent files with name, info hash, size, and tracker metadata",
		Factory:     newPlugin,
		Validate:    validate,
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

func (p *torrentPlugin) Annotate(ctx context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	data, err := p.readTorrent(ctx, e)
	if err != nil {
		return fmt.Errorf("metainfo_torrent: %w", err)
	}
	if data == nil {
		return nil // not a torrent entry
	}

	ti, err := bencode.DecodeTorrent(data)
	if err != nil {
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
	return nil
}

// readTorrent returns raw torrent bytes from a local file or by downloading the
// URL. Returns (nil, nil) if this entry does not appear to be a .torrent.
func (p *torrentPlugin) readTorrent(ctx context.Context, e *entry.Entry) ([]byte, error) {
	// Prefer local file (set by filesystem input plugin).
	if loc := e.GetString(entry.FieldFileLocation); loc != "" && strings.HasSuffix(strings.ToLower(loc), ".torrent") {
		return os.ReadFile(loc)
	}
	// Fall back to URL.
	if !strings.HasSuffix(strings.ToLower(e.URL), ".torrent") {
		return nil, nil
	}
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
		return nil, fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, e.URL)
	}
	return io.ReadAll(resp.Body)
}
