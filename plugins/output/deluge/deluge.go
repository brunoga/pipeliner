// Package deluge sends torrents to a Deluge daemon via its Web UI JSON-RPC API.
package deluge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/interp"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "deluge",
		Description: "add torrents to a Deluge daemon via JSON-RPC",
		PluginPhase: plugin.PhaseOutput,
		Factory:     newPlugin,
	})
}

type delugePlugin struct {
	endpoint string // e.g. "http://host:8112/json"
	password string
	pathIP   *interp.Interpolator
	client   *http.Client
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	host, _ := cfg["host"].(string)
	if host == "" {
		host = "localhost"
	}
	port := intVal(cfg["port"], 8112)
	password, _ := cfg["password"].(string)

	pathPat, _ := cfg["path"].(string)
	if pathPat == "" {
		pathPat = "{download_path}"
	}
	pathIP, err := interp.Compile(pathPat)
	if err != nil {
		return nil, fmt.Errorf("deluge: invalid path pattern: %w", err)
	}

	scheme := "http"
	if tls, _ := cfg["tls"].(bool); tls {
		scheme = "https"
	}

	return &delugePlugin{
		endpoint: fmt.Sprintf("%s://%s:%d/json", scheme, host, port),
		password: password,
		pathIP:   pathIP,
		client:   &http.Client{},
	}, nil
}

func (p *delugePlugin) Name() string        { return "deluge" }
func (p *delugePlugin) Phase() plugin.Phase { return plugin.PhaseOutput }

func (p *delugePlugin) Output(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	if err := p.login(ctx); err != nil {
		return fmt.Errorf("deluge: login: %w", err)
	}
	for _, e := range entries {
		savePath, err := p.renderPath(e)
		if err != nil {
			tc.Logger.Error("deluge: render path", "title", e.Title, "err", err)
			continue
		}
		if err := p.addTorrent(ctx, e.URL, savePath); err != nil {
			tc.Logger.Error("deluge: add torrent", "title", e.Title, "err", err)
		}
	}
	return nil
}

func (p *delugePlugin) login(ctx context.Context) error {
	result, err := p.rpc(ctx, "auth.login", []any{p.password})
	if err != nil {
		return err
	}
	if ok, _ := result.(bool); !ok {
		return fmt.Errorf("authentication failed")
	}
	return nil
}

func (p *delugePlugin) addTorrent(ctx context.Context, url, savePath string) error {
	opts := map[string]any{}
	if savePath != "" {
		opts["download_location"] = savePath
	}
	_, err := p.rpc(ctx, "core.add_torrent_url", []any{url, opts})
	return err
}

// rpc sends a single JSON-RPC 2.0 call and returns the result field.
func (p *delugePlugin) rpc(ctx context.Context, method string, params []any) (any, error) {
	payload := map[string]any{
		"method": method,
		"params": params,
		"id":     1,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rpcResp struct {
		Result any    `json:"result"`
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

func (p *delugePlugin) renderPath(e *entry.Entry) (string, error) {
	return p.pathIP.Render(interp.EntryData(e))
}

func intVal(v any, def int) int {
	switch t := v.(type) {
	case int:
		return t
	case float64:
		return int(t)
	}
	return def
}
