package torrentclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// qbittorrentClient talks to qBittorrent's Web API v2. Login and error
// conventions mirror the qbittorrent sink (200 with body "Fails." on bad
// credentials, cookie-jar session).
type qbittorrentClient struct {
	baseURL  string
	username string
	password string
	client   *http.Client
}

func newQBittorrentClient(cfg Config) *qbittorrentClient {
	port := cfg.Port
	if port == 0 {
		port = 8080
	}
	scheme := "http"
	if cfg.TLS {
		scheme = "https"
	}
	jar, _ := cookiejar.New(nil)
	return &qbittorrentClient{
		baseURL:  fmt.Sprintf("%s://%s:%d", scheme, cfg.Host, port),
		username: cfg.Username,
		password: cfg.Password,
		client:   &http.Client{Jar: jar, Timeout: 30 * time.Second},
	}
}

func (c *qbittorrentClient) login(ctx context.Context) error {
	form := url.Values{
		"username": {c.username},
		"password": {c.password},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/v2/auth/login",
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login: HTTP %d", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) == "Fails." {
		return fmt.Errorf("authentication failed")
	}
	return nil
}

type qbtTorrent struct {
	Hash         string  `json:"hash"`
	Name         string  `json:"name"`
	State        string  `json:"state"`
	Ratio        float64 `json:"ratio"`
	SeedingTime  int64   `json:"seeding_time"` // seconds
	AddedOn      int64   `json:"added_on"`     // unix
	LastActivity int64   `json:"last_activity"`
	Progress     float64 `json:"progress"` // 0-1
	SavePath     string  `json:"save_path"`
}

func (c *qbittorrentClient) ListTorrents(ctx context.Context) ([]Torrent, error) {
	if err := c.login(ctx); err != nil {
		return nil, fmt.Errorf("qbittorrent: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/v2/torrents/info", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qbittorrent: torrents/info: HTTP %d", resp.StatusCode)
	}
	var raw []qbtTorrent
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("qbittorrent: decode torrents/info: %w", err)
	}
	out := make([]Torrent, 0, len(raw))
	for _, t := range raw {
		out = append(out, normalizeQBittorrent(t))
	}
	return out, nil
}

func normalizeQBittorrent(t qbtTorrent) Torrent {
	nt := Torrent{
		Hash:        strings.ToLower(t.Hash),
		Name:        t.Name,
		SeedTime:    time.Duration(t.SeedingTime) * time.Second,
		Progress:    t.Progress * 100,
		DownloadDir: t.SavePath,
	}
	if t.Ratio > 0 {
		nt.Ratio = t.Ratio
	}
	if t.AddedOn > 0 {
		nt.AddedAt = time.Unix(t.AddedOn, 0)
	}
	if t.LastActivity > 0 {
		nt.LastActivity = time.Unix(t.LastActivity, 0)
	}
	nt.State = normalizeQbtState(t.State)
	if nt.State == StateErrored {
		// The torrents/info payload carries no error message; surface the
		// native state name so users can tell "error" from "missingFiles".
		nt.Error = "qBittorrent state: " + t.State
	}
	return nt
}

// normalizeQbtState maps qBittorrent's native state names onto the shared
// State enum. "stopped*" variants are the qBittorrent 5.x renames of
// "paused*".
func normalizeQbtState(state string) State {
	switch state {
	case "error", "missingFiles":
		return StateErrored
	case "stalledDL":
		return StateStalled
	case "pausedDL", "pausedUP", "stoppedDL", "stoppedUP":
		return StatePaused
	case "checkingDL", "checkingUP", "checkingResumeData", "moving", "allocating":
		return StateChecking
	case "uploading", "stalledUP", "forcedUP", "queuedUP":
		return StateSeeding
	default:
		// downloading, metaDL, forcedDL, queuedDL, unknown states
		return StateDownloading
	}
}

func (c *qbittorrentClient) Remove(ctx context.Context, hashes []string, withData bool) error {
	if len(hashes) == 0 {
		return nil
	}
	form := url.Values{
		"hashes":      {strings.Join(hashes, "|")},
		"deleteFiles": {fmt.Sprintf("%t", withData)},
	}
	return c.post(ctx, "/api/v2/torrents/delete", form)
}

func (c *qbittorrentClient) Pause(ctx context.Context, hashes []string) error {
	if len(hashes) == 0 {
		return nil
	}
	form := url.Values{"hashes": {strings.Join(hashes, "|")}}
	// qBittorrent 5.x (Web API ≥ 2.11) renamed torrents/pause to
	// torrents/stop; try the long-standing endpoint first and fall back.
	err := c.post(ctx, "/api/v2/torrents/pause", form)
	if err != nil && strings.Contains(err.Error(), "HTTP 404") {
		return c.post(ctx, "/api/v2/torrents/stop", form)
	}
	return err
}

func (c *qbittorrentClient) Reannounce(ctx context.Context, hashes []string) error {
	if len(hashes) == 0 {
		return nil
	}
	form := url.Values{"hashes": {strings.Join(hashes, "|")}}
	return c.post(ctx, "/api/v2/torrents/reannounce", form)
}

func (c *qbittorrentClient) post(ctx context.Context, path string, form url.Values) error {
	if err := c.login(ctx); err != nil {
		return fmt.Errorf("qbittorrent: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qbittorrent: %s: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
