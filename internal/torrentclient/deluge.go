package torrentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"time"
)

type delugeClient struct {
	endpoint string
	password string
	client   *http.Client
}

func newDelugeClient(cfg Config) *delugeClient {
	scheme := "http"
	if cfg.TLS {
		scheme = "https"
	}
	port := cfg.Port
	if port == 0 {
		port = 8112
	}
	jar, _ := cookiejar.New(nil)
	return &delugeClient{
		endpoint: fmt.Sprintf("%s://%s:%d/json", scheme, cfg.Host, port),
		password: cfg.Password,
		client:   &http.Client{Jar: jar, Timeout: 15 * time.Second},
	}
}

func (c *delugeClient) login(ctx context.Context) error {
	result, err := c.rpc(ctx, "auth.login", []any{c.password})
	if err != nil {
		return err
	}
	if ok, _ := result.(bool); !ok {
		return fmt.Errorf("authentication failed")
	}
	return nil
}

func (c *delugeClient) rpc(ctx context.Context, method string, params []any) (any, error) {
	payload := map[string]any{
		"method": method,
		"params": params,
		"id":     1,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var rpcResp struct {
		Result any `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("RPC error: %s", rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

func (c *delugeClient) ListTorrents(ctx context.Context) ([]Torrent, error) {
	if err := c.login(ctx); err != nil {
		return nil, fmt.Errorf("deluge: login: %w", err)
	}
	keys := []string{"name", "state", "progress", "ratio", "time_added", "seeding_time", "download_location", "message"}
	res, err := c.rpc(ctx, "core.get_torrents_status", []any{map[string]any{}, keys})
	if err != nil {
		return nil, fmt.Errorf("deluge: get_torrents_status: %w", err)
	}

	// Result is a map of hash -> map of keys
	torrentsMap, ok := res.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("deluge: unexpected result type")
	}

	var torrents []Torrent
	for hash, data := range torrentsMap {
		fields, ok := data.(map[string]any)
		if !ok {
			continue
		}
		
		name, _ := fields["name"].(string)
		stateStr, _ := fields["state"].(string)
		progress, _ := fields["progress"].(float64)
		ratio, _ := fields["ratio"].(float64)
		timeAddedF, _ := fields["time_added"].(float64)
		seedingTimeF, _ := fields["seeding_time"].(float64)
		downloadLoc, _ := fields["download_location"].(string)
		message, _ := fields["message"].(string)

		state := StateDownloading
		switch stateStr {
		case "Downloading":
			state = StateDownloading
		case "Seeding":
			state = StateSeeding
		case "Paused":
			state = StatePaused
		case "Error":
			state = StateErrored
		case "Checking", "Allocating", "Moving":
			state = StateChecking
		default:
			// Deluge doesn't have a distinct "stalled" state string, it's usually "Downloading" or "Seeding"
			// But we'll just map unknown to downloading. Wait, what about Queued?
			state = StateDownloading
		}

		if ratio < 0 {
			ratio = 0
		}

		torrents = append(torrents, Torrent{
			Hash:        hash,
			Name:        name,
			State:       state,
			Error:       message,
			Ratio:       ratio,
			SeedTime:    time.Duration(seedingTimeF) * time.Second,
			AddedAt:     time.Unix(int64(timeAddedF), 0),
			Progress:    progress,
			DownloadDir: downloadLoc,
		})
	}
	return torrents, nil
}

func (c *delugeClient) Remove(ctx context.Context, hashes []string, withData bool) error {
	if err := c.login(ctx); err != nil {
		return fmt.Errorf("deluge: login: %w", err)
	}
	for _, hash := range hashes {
		_, err := c.rpc(ctx, "core.remove_torrent", []any{hash, withData})
		if err != nil {
			return fmt.Errorf("deluge: remove_torrent %s: %w", hash, err)
		}
	}
	return nil
}

func (c *delugeClient) Pause(ctx context.Context, hashes []string) error {
	if err := c.login(ctx); err != nil {
		return fmt.Errorf("deluge: login: %w", err)
	}
	if len(hashes) == 0 {
		return nil
	}
	_, err := c.rpc(ctx, "core.pause_torrent", []any{hashes})
	if err != nil {
		return fmt.Errorf("deluge: pause_torrent: %w", err)
	}
	return nil
}

func (c *delugeClient) Reannounce(ctx context.Context, hashes []string) error {
	if err := c.login(ctx); err != nil {
		return fmt.Errorf("deluge: login: %w", err)
	}
	if len(hashes) == 0 {
		return nil
	}
	_, err := c.rpc(ctx, "core.force_reannounce", []any{hashes})
	if err != nil {
		return fmt.Errorf("deluge: force_reannounce: %w", err)
	}
	return nil
}
