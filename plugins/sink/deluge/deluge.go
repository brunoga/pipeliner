// Package deluge sends torrents to a Deluge daemon via its Web UI JSON-RPC API.
package deluge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/grabs"
	"github.com/brunoga/pipeliner/internal/interp"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "deluge",
		Description: "add torrents to a Deluge daemon via JSON-RPC",
		Role:        plugin.RoleSink,
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "host", Type: plugin.FieldTypeString, Default: "localhost", Hint: "Deluge daemon host"},
			{Key: "port", Type: plugin.FieldTypeInt, Default: 8112, Hint: "Deluge JSON-RPC port"},
			{Key: "password", Type: plugin.FieldTypeString, Hint: "Deluge Web UI password"},
			{Key: "path", Type: plugin.FieldTypePattern, Hint: "Download location template, e.g. /data/{title}"},
			{Key: "move_completed_path", Type: plugin.FieldTypePattern, Hint: "Move-completed path template"},
			{Key: "tls", Type: plugin.FieldTypeBool, Hint: "Use HTTPS to connect"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if v, ok := cfg["port"]; ok {
		if n := intVal(v, 0); n <= 0 {
			errs = append(errs, fmt.Errorf("deluge: \"port\" must be a positive integer"))
		}
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "deluge", "host", "port", "password", "path", "move_completed_path", "tls")...)
	return errs
}

type delugePlugin struct {
	endpoint        string // e.g. "http://host:8112/json"
	password        string
	pathIP          *interp.Interpolator
	moveCompletedIP *interp.Interpolator // nil = don't set move_completed
	client          *http.Client
	grabStore       *grabs.Store
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	host, _ := cfg["host"].(string)
	if host == "" {
		host = "localhost"
	}
	port := intVal(cfg["port"], 8112)
	password, _ := cfg["password"].(string)

	var pathIP *interp.Interpolator
	if pathPat, _ := cfg["path"].(string); pathPat != "" {
		ip, err := interp.Compile(pathPat)
		if err != nil {
			return nil, fmt.Errorf("deluge: invalid path pattern: %w", err)
		}
		pathIP = ip
	}

	var moveCompletedIP *interp.Interpolator
	if mcPat, _ := cfg["move_completed_path"].(string); mcPat != "" {
		ip, err := interp.Compile(mcPat)
		if err != nil {
			return nil, fmt.Errorf("deluge: invalid move_completed_path pattern: %w", err)
		}
		moveCompletedIP = ip
	}

	scheme := "http"
	if tls, _ := cfg["tls"].(bool); tls {
		scheme = "https"
	}

	var grabStore *grabs.Store
	if db != nil {
		grabStore = grabs.NewStore(db.Bucket(grabs.BucketName))
	}

	jar, _ := cookiejar.New(nil)
	return &delugePlugin{
		endpoint:        fmt.Sprintf("%s://%s:%d/json", scheme, host, port),
		password:        password,
		pathIP:          pathIP,
		moveCompletedIP: moveCompletedIP,
		client:          &http.Client{Jar: jar},
		grabStore:       grabStore,
	}, nil
}

func (p *delugePlugin) Name() string { return "deluge" }

func (p *delugePlugin) deliver(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	if err := p.login(ctx); err != nil {
		for _, e := range entries {
			e.Fail("deluge: login failed")
		}
		return fmt.Errorf("deluge: login: %w", err)
	}
	for _, e := range entries {
		savePath, err := p.renderPath(e)
		if err != nil {
			tc.Logger.Error("deluge: render path", "title", e.Title, "err", err)
			e.Fail("deluge: render path: " + err.Error())
			continue
		}
		var moveCompleted string
		if p.moveCompletedIP != nil {
			moveCompleted, err = p.moveCompletedIP.Render(interp.EntryData(e))
			if err != nil {
				tc.Logger.Error("deluge: render move_completed_path", "title", e.Title, "err", err)
				e.Fail("deluge: render move_completed_path: " + err.Error())
				continue
			}
		}
		linkType := e.GetString(entry.FieldTorrentLinkType)
		if err := p.addTorrent(ctx, e.URL, linkType, savePath, moveCompleted); err != nil {
			if strings.Contains(err.Error(), "already in session") {
				// Torrent is already in Deluge (e.g. manually added). Mark the
				// entry consumed so chained notification sinks (email, etc.) are
				// silenced, but leave State = Accepted so CommitPlugin.Commit still
				// runs — this ensures the URL is learned by the seen plugin and
				// won't be retried on the next run.
				tc.Logger.Info("deluge: torrent already in session, skipping notifications", "title", e.Title)
				e.Consume()
			} else {
				err = summarizeAddError(e.URL, err)
				tc.Logger.Error("deluge: add torrent",
					"title", e.Title, "url", e.URL, "link_type", linkType, "err", err)
				e.Fail("deluge: " + err.Error())
			}
		} else {
			p.recordGrab(tc, e)
		}
	}
	return nil
}

// recordGrab links the torrent back to its release URL so mark_failed can 
// recover it if the torrent dies.
func (p *delugePlugin) recordGrab(tc *plugin.TaskContext, e *entry.Entry) {
	if p.grabStore == nil {
		return
	}
	hash := grabs.HashForEntry(e)
	if hash == "" {
		tc.Logger.Debug("deluge: no info-hash for grab record", "entry", e.Title)
		return
	}
	if err := p.grabStore.Put(hash, grabs.FromEntry(e, tc.Name)); err != nil {
		tc.Logger.Warn("deluge: record grab", "entry", e.Title, "err", err)
	}
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

func (p *delugePlugin) addTorrent(ctx context.Context, rawURL, linkType, savePath, moveCompletedPath string) error {
	if err := validateTorrentURL(rawURL); err != nil {
		return err
	}
	opts := map[string]any{}
	if savePath != "" {
		opts["download_location"] = savePath
	}
	if moveCompletedPath != "" {
		opts["move_completed"] = true
		opts["move_completed_path"] = moveCompletedPath
	}
	// Check torrent_link_type first (set by sources such as Jackett that know
	// the type without an HTTP fetch), then fall back to URL prefix inspection.
	method := "core.add_torrent_url"
	if linkType == "magnet" || (linkType == "" && strings.HasPrefix(rawURL, "magnet:")) {
		method = "core.add_torrent_magnet"
	}
	_, err := p.rpc(ctx, method, []any{rawURL, opts})
	return err
}

// validateTorrentURL rejects URLs that the Deluge daemon cannot act on. The
// Twisted HTTP downloader inside Deluge raises a verbose
// `twisted.web.error.SchemeNotSupported: Unsupported scheme: b''`
// traceback for empty / scheme-less / host-less URLs, so we surface a
// useful error here before the RPC is made.
func validateTorrentURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("entry has empty URL")
	}
	if strings.HasPrefix(rawURL, "magnet:") {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q (full URL: %q)", u.Scheme, rawURL)
	}
	if u.Host == "" {
		return fmt.Errorf("URL has no host: %q", rawURL)
	}
	return nil
}

// summarizeAddError replaces deluge / Twisted error walls-of-text with a
// concise one-liner that names the URL and the likely cause. The RPC error
// body for a malformed download URL is a multi-line Python traceback, which
// is useless in the entry FailReason and floods the log. We pattern-match
// on the well-known terminal exception class and rewrite the message; the
// original is still emitted at debug level by the caller via slog.
func summarizeAddError(rawURL string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "SchemeNotSupported: Unsupported scheme: b''"):
		return fmt.Errorf("deluge could not download torrent from %q (empty URL scheme parsed by Python — likely the indexer redirected to a target with an empty Location header or a non-HTTP scheme)", rawURL)
	case strings.Contains(msg, "SchemeNotSupported: Unsupported scheme:"):
		// scheme is included in the original message after the colon
		return fmt.Errorf("deluge could not download torrent from %q (indexer redirected to an unsupported scheme; original: %s)", rawURL, oneLine(msg))
	}
	return err
}

// oneLine collapses any newlines into spaces so a multi-line traceback fits
// on a single log line.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
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

func (p *delugePlugin) renderPath(e *entry.Entry) (string, error) {
	if p.pathIP == nil {
		return "", nil
	}
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

func (p *delugePlugin) Consume(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	if tc.DryRun {
		return nil
	}
	return p.deliver(ctx, tc, entries)
}
