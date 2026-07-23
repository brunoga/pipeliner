package torrentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// transmissionClient talks to Transmission's JSON-RPC API. The session-id
// handshake (409 + X-Transmission-Session-Id) mirrors the transmission sink.
type transmissionClient struct {
	endpoint  string
	username  string
	password  string
	client    *http.Client
	mu        sync.Mutex
	sessionID string
}

func newTransmissionClient(cfg Config) *transmissionClient {
	port := cfg.Port
	if port == 0 {
		port = 9091
	}
	rpcPath := cfg.RPCPath
	if rpcPath == "" {
		rpcPath = "/transmission/rpc"
	}
	return &transmissionClient{
		endpoint: fmt.Sprintf("http://%s:%d%s", cfg.Host, port, rpcPath),
		username: cfg.Username,
		password: cfg.Password,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// Transmission "status" codes (torrent-get).
const (
	trStatusStopped      = 0
	trStatusCheckWait    = 1
	trStatusCheck        = 2
	trStatusDownloadWait = 3
	trStatusDownload     = 4
	trStatusSeedWait     = 5
	trStatusSeed         = 6
)

type trTorrent struct {
	HashString     string  `json:"hashString"`
	Name           string  `json:"name"`
	Status         int     `json:"status"`
	Error          int     `json:"error"`
	ErrorString    string  `json:"errorString"`
	IsStalled      bool    `json:"isStalled"`
	PercentDone    float64 `json:"percentDone"`
	UploadRatio    float64 `json:"uploadRatio"`
	SecondsSeeding int64   `json:"secondsSeeding"`
	AddedDate      int64   `json:"addedDate"`
	ActivityDate   int64   `json:"activityDate"`
	DownloadDir    string  `json:"downloadDir"`
}

func (c *transmissionClient) ListTorrents(ctx context.Context) ([]Torrent, error) {
	args := map[string]any{
		"fields": []string{
			"hashString", "name", "status", "error", "errorString",
			"isStalled", "percentDone", "uploadRatio", "secondsSeeding",
			"addedDate", "activityDate", "downloadDir",
		},
	}
	var result struct {
		Arguments struct {
			Torrents []trTorrent `json:"torrents"`
		} `json:"arguments"`
		Result string `json:"result"`
	}
	if err := c.rpc(ctx, "torrent-get", args, &result); err != nil {
		return nil, err
	}
	if result.Result != "success" {
		return nil, fmt.Errorf("transmission: torrent-get: %s", result.Result)
	}

	out := make([]Torrent, 0, len(result.Arguments.Torrents))
	for _, t := range result.Arguments.Torrents {
		out = append(out, normalizeTransmission(t))
	}
	return out, nil
}

func normalizeTransmission(t trTorrent) Torrent {
	nt := Torrent{
		Hash:        strings.ToLower(t.HashString),
		Name:        t.Name,
		Error:       t.ErrorString,
		SeedTime:    time.Duration(t.SecondsSeeding) * time.Second,
		Progress:    t.PercentDone * 100,
		DownloadDir: t.DownloadDir,
	}
	if t.UploadRatio > 0 { // -1 = not available, -2 = infinite
		nt.Ratio = t.UploadRatio
	}
	if t.AddedDate > 0 {
		nt.AddedAt = time.Unix(t.AddedDate, 0)
	}
	if t.ActivityDate > 0 {
		nt.LastActivity = time.Unix(t.ActivityDate, 0)
	}

	switch {
	case t.Error != 0:
		nt.State = StateErrored
		if nt.Error == "" {
			nt.Error = fmt.Sprintf("transmission error code %d", t.Error)
		}
	case t.Status == trStatusStopped:
		nt.State = StatePaused
	case t.Status == trStatusCheckWait || t.Status == trStatusCheck:
		nt.State = StateChecking
	case t.Status == trStatusSeedWait || t.Status == trStatusSeed:
		nt.State = StateSeeding
	case t.IsStalled && t.PercentDone < 1:
		nt.State = StateStalled
	default: // download / download-wait
		nt.State = StateDownloading
	}
	return nt
}

func (c *transmissionClient) Remove(ctx context.Context, hashes []string, withData bool) error {
	if len(hashes) == 0 {
		return nil
	}
	return c.control(ctx, "torrent-remove", map[string]any{
		"ids":               hashes,
		"delete-local-data": withData,
	})
}

func (c *transmissionClient) Pause(ctx context.Context, hashes []string) error {
	if len(hashes) == 0 {
		return nil
	}
	return c.control(ctx, "torrent-stop", map[string]any{"ids": hashes})
}

func (c *transmissionClient) Reannounce(ctx context.Context, hashes []string) error {
	if len(hashes) == 0 {
		return nil
	}
	return c.control(ctx, "torrent-reannounce", map[string]any{"ids": hashes})
}

// control runs an RPC method that returns no payload beyond the result string.
func (c *transmissionClient) control(ctx context.Context, method string, args map[string]any) error {
	var result struct {
		Result string `json:"result"`
	}
	if err := c.rpc(ctx, method, args, &result); err != nil {
		return err
	}
	if result.Result != "success" {
		return fmt.Errorf("transmission: %s: %s", method, result.Result)
	}
	return nil
}

// rpc calls a Transmission JSON-RPC method, handling the 409 session-id
// challenge on first call and on expiry (same protocol as the sink).
func (c *transmissionClient) rpc(ctx context.Context, method string, args, result any) error {
	body := map[string]any{
		"method":    method,
		"arguments": args,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	for range 2 {
		resp, err := c.doRequest(ctx, data)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusConflict { // 409 → refresh session id
			newID := resp.Header.Get("X-Transmission-Session-Id")
			resp.Body.Close()
			c.mu.Lock()
			c.sessionID = newID
			c.mu.Unlock()
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP %d from Transmission", resp.StatusCode)
		}
		if result == nil {
			return nil
		}
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return fmt.Errorf("transmission: could not establish session after retry")
}

func (c *transmissionClient) doRequest(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.mu.Lock()
	sid := c.sessionID
	c.mu.Unlock()
	if sid != "" {
		req.Header.Set("X-Transmission-Session-Id", sid)
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	return c.client.Do(req)
}
