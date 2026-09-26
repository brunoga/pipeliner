// Package torrentclient provides a shared session-query and control client
// for BitTorrent daemons. It powers the torrent_session source and the
// torrent_control sink; the add-torrent paths stay inside the respective
// sink plugins (transmission, qbittorrent), which keep their own minimal
// clients tuned for adding.
//
// Supported backends: Transmission (JSON-RPC), qBittorrent (Web API v2), and Deluge.
package torrentclient

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// State is a client-agnostic torrent state. Every backend's native state is
// normalized to one of these values.
type State string

const (
	StateDownloading State = "downloading"
	StateSeeding     State = "seeding"
	StateStalled     State = "stalled" // downloading but no peer activity
	StatePaused      State = "paused"
	StateErrored     State = "errored"
	StateChecking    State = "checking" // verifying, allocating, or moving data
)

// States lists every normalized state value, for schemas and docs.
var States = []string{
	string(StateDownloading),
	string(StateSeeding),
	string(StateStalled),
	string(StatePaused),
	string(StateErrored),
	string(StateChecking),
}

// Torrent is one torrent in the client's session, normalized across backends.
type Torrent struct {
	// Hash is the lowercase hex info-hash.
	Hash string
	// Name is the torrent display name.
	Name string
	// State is the normalized state.
	State State
	// Error carries the client's error message when State == StateErrored.
	// May be empty when the backend reports an error state without a message
	// (qBittorrent).
	Error string
	// Ratio is the upload ratio. Negative backend sentinels (Transmission's
	// -1 "not available") are clamped to 0.
	Ratio float64
	// SeedTime is the cumulative seeding time.
	SeedTime time.Duration
	// AddedAt is when the torrent was added to the session.
	AddedAt time.Time
	// LastActivity is the last upload/download activity; zero when the
	// backend has no record of any activity.
	LastActivity time.Time
	// Progress is the download completion percentage, 0-100.
	Progress float64
	// DownloadDir is the directory the torrent's data is stored in.
	DownloadDir string
	// Size is the total size of the torrent's data in bytes.
	Size int64
	// Downloaded and Uploaded are the all-time payload byte counters for
	// this torrent, as the client has them. They survive a restart but not
	// a re-add.
	Downloaded int64
	Uploaded   int64
	// DownloadRate and UploadRate are the current payload transfer rates
	// in bytes per second.
	DownloadRate int64
	UploadRate   int64
	// Seeds is the number of connected peers that have the complete
	// torrent; Peers is the number of connected peers that do not. Both
	// count live connections, not the swarm size the tracker reports, so
	// Seeds == 0 on a downloading torrent means nothing is being served to
	// this client right now.
	Seeds int
	Peers int
	// ETA is the client's estimate of the time left to complete the
	// download. Zero when the torrent is finished, idle, or the backend
	// cannot estimate it — every backend spells "unknown" differently, so
	// the sentinels are normalized away here.
	ETA time.Duration
	// Label is the client-side category or label, empty when unset. On
	// Deluge this requires the Label plugin to be enabled.
	Label string
	// Tracker is the host of the torrent's primary tracker, empty when the
	// backend does not report one.
	Tracker string
	// CompletedAt is when the download finished; zero while incomplete.
	CompletedAt time.Time
}

// Client is the common session-query and control interface.
type Client interface {
	// ListTorrents returns every torrent in the client's session.
	ListTorrents(ctx context.Context) ([]Torrent, error)
	// Remove removes the torrents with the given info-hashes from the
	// session. When withData is true the downloaded files are deleted too.
	Remove(ctx context.Context, hashes []string, withData bool) error
	// Pause stops/pauses the torrents with the given info-hashes.
	Pause(ctx context.Context, hashes []string) error
	// Reannounce forces a tracker reannounce for the given info-hashes.
	Reannounce(ctx context.Context, hashes []string) error
}

// Backend names accepted by New. They match the corresponding sink plugin
// names so configs read uniformly.
const (
	BackendTransmission = "transmission"
	BackendQBittorrent  = "qbittorrent"
	BackendDeluge       = "deluge"
)

// Backends lists the supported backend names, for schemas and validation.
var Backends = []string{BackendTransmission, BackendQBittorrent, BackendDeluge}

// Config carries the connection settings shared by all backends. Key names
// mirror the corresponding sink plugin's config keys.
type Config struct {
	Host     string // default "localhost"
	Port     int    // default: backend-specific (9091 transmission, 8080 qbittorrent)
	Username string
	Password string
	// TLS switches to https (qBittorrent only, mirrors the sink's "tls" key).
	TLS bool
	// RPCPath is the Transmission RPC endpoint path
	// (default "/transmission/rpc", mirrors the sink's "rpc_path" key).
	RPCPath string
}

// ConfigFromMap builds a Config from a plugin configuration block using the
// shared connection key names (host, port, username, password, tls,
// rpc_path). Both torrent_session and torrent_control accept the same keys.
func ConfigFromMap(cfg map[string]any) Config {
	host, _ := cfg["host"].(string)
	username, _ := cfg["username"].(string)
	password, _ := cfg["password"].(string)
	rpcPath, _ := cfg["rpc_path"].(string)
	tls, _ := cfg["tls"].(bool)
	port := 0
	switch n := cfg["port"].(type) {
	case int:
		port = n
	case int64:
		port = int(n)
	case float64:
		port = int(n)
	}
	return Config{
		Host:     host,
		Port:     port,
		Username: username,
		Password: password,
		TLS:      tls,
		RPCPath:  rpcPath,
	}
}

// New returns a Client for the named backend.
func New(backend string, cfg Config) (Client, error) {
	if cfg.Host == "" {
		cfg.Host = "localhost"
	}
	switch backend {
	case BackendTransmission:
		return newTransmissionClient(cfg), nil
	case BackendQBittorrent:
		return newQBittorrentClient(cfg), nil
	case BackendDeluge:
		return newDelugeClient(cfg), nil
	default:
		return nil, fmt.Errorf("torrentclient: unsupported backend %q (supported: %s, %s, %s)",
			backend, BackendTransmission, BackendQBittorrent, BackendDeluge)
	}
}

// trackerHost reduces a tracker announce URL to its host, so the Tracker
// field reads the same across backends: Transmission and Deluge report a
// bare host already, qBittorrent reports the full announce URL. A value
// that does not parse as a URL is returned unchanged — it is usually
// already a host, and a tracker string is worth surfacing either way.
func trackerHost(announce string) string {
	if announce == "" {
		return ""
	}
	u, err := url.Parse(announce)
	if err != nil || u.Hostname() == "" {
		return announce
	}
	return u.Hostname()
}
