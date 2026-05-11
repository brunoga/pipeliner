// Package transmission implements an output plugin that adds torrents to a
// Transmission BitTorrent client via its JSON-RPC API.
//
// Transmission's RPC requires a session-id header obtained by making an initial
// request that returns HTTP 409 with the X-Transmission-Session-Id header. The
// plugin automatically handles this handshake and retries on session expiry.
//
// Config keys:
//
//	host     - Transmission host (default: "localhost")
//	port     - RPC port (default: 9091)
//	username - HTTP basic auth username (optional)
//	password - HTTP basic auth password (optional)
//	path     - download directory pattern; if omitted the client's default is used
//	paused   - add torrent in paused state (default: false)
//	rpc_path - RPC endpoint path (default: "/transmission/rpc")
package transmission

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/interp"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "transmission",
		PluginPhase: plugin.PhaseOutput,
		Role:        plugin.RoleSink,
		Description: "Adds accepted torrents to a Transmission client via JSON-RPC",
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "host", Type: plugin.FieldTypeString, Default: "localhost", Hint: "Transmission host"},
			{Key: "port", Type: plugin.FieldTypeInt, Default: 9091, Hint: "Transmission RPC port"},
			{Key: "username", Type: plugin.FieldTypeString, Hint: "HTTP basic auth username"},
			{Key: "password", Type: plugin.FieldTypeString, Hint: "HTTP basic auth password"},
			{Key: "path", Type: plugin.FieldTypePattern, Hint: "Download directory template, e.g. /data/{title}"},
			{Key: "paused", Type: plugin.FieldTypeBool, Hint: "Add torrent in paused state"},
			{Key: "rpc_path", Type: plugin.FieldTypeString, Default: "/transmission/rpc", Hint: "RPC endpoint path"},
		},
	})
}

func validate(cfg map[string]any) []error {
	return plugin.OptUnknownKeys(cfg, "transmission", "host", "port", "username", "password", "path", "paused", "rpc_path")
}

type transmissionPlugin struct {
	endpoint  string
	username  string
	password  string
	pathIP    *interp.Interpolator
	paused    bool
	client    *http.Client
	mu        sync.Mutex
	sessionID string
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	host := "localhost"
	if v, ok := cfg["host"].(string); ok && v != "" {
		host = v
	}
	port := 9091
	if v, ok := cfg["port"]; ok {
		switch n := v.(type) {
		case int:
			port = n
		case int64:
			port = int(n)
		case float64:
			port = int(n)
		}
	}
	rpcPath := "/transmission/rpc"
	if v, ok := cfg["rpc_path"].(string); ok && v != "" {
		rpcPath = v
	}

	var pathIP *interp.Interpolator
	if v, ok := cfg["path"].(string); ok && v != "" {
		ip, err := interp.Compile(v)
		if err != nil {
			return nil, fmt.Errorf("transmission: invalid path pattern: %w", err)
		}
		pathIP = ip
	}

	paused := false
	if v, ok := cfg["paused"].(bool); ok {
		paused = v
	}

	return &transmissionPlugin{
		endpoint: fmt.Sprintf("http://%s:%d%s", host, port, rpcPath),
		username: stringVal(cfg, "username"),
		password: stringVal(cfg, "password"),
		pathIP:   pathIP,
		paused:   paused,
		client:   &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func stringVal(cfg map[string]any, key string) string {
	v, _ := cfg[key].(string)
	return v
}

func (p *transmissionPlugin) Name() string        { return "transmission" }
func (p *transmissionPlugin) Phase() plugin.Phase { return plugin.PhaseOutput }

func (p *transmissionPlugin) Output(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	for _, e := range entries {
		if err := p.addTorrent(ctx, e); err != nil {
			tc.Logger.Warn("transmission: failed to add torrent", "entry", e.Title, "err", err)
			e.Fail("transmission: " + err.Error())
		}
	}
	return nil
}

func (p *transmissionPlugin) addTorrent(ctx context.Context, e *entry.Entry) error {
	args := map[string]any{
		"filename": e.URL,
		"paused":   p.paused,
	}
	if p.pathIP != nil {
		downloadDir, err := p.pathIP.Render(interp.EntryData(e))
		if err != nil {
			return fmt.Errorf("render path: %w", err)
		}
		if downloadDir != "" {
			args["download-dir"] = downloadDir
		}
	}

	return p.rpc(ctx, "torrent-add", args, nil)
}

// rpc calls the Transmission JSON-RPC method with the given arguments.
// It automatically handles the 409 session-id challenge on first call and on expiry.
func (p *transmissionPlugin) rpc(ctx context.Context, method string, args, result any) error {
	body := map[string]any{
		"method":    method,
		"arguments": args,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	// Attempt up to two times: once with cached session-id, once after refresh.
	for range 2 {
		resp, err := p.doRequest(ctx, data)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusConflict { // 409 → refresh session id
			newID := resp.Header.Get("X-Transmission-Session-Id")
			resp.Body.Close()
			p.mu.Lock()
			p.sessionID = newID
			p.mu.Unlock()
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

func (p *transmissionPlugin) doRequest(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	p.mu.Lock()
	sid := p.sessionID
	p.mu.Unlock()
	if sid != "" {
		req.Header.Set("X-Transmission-Session-Id", sid)
	}
	if p.username != "" {
		req.SetBasicAuth(p.username, p.password)
	}
	return p.client.Do(req)
}

