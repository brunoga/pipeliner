// Package qbittorrent adds torrents to a qBittorrent daemon via its Web API v2.
package qbittorrent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
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
		PluginName:  "qbittorrent",
		Description: "add torrents to a qBittorrent daemon via Web API v2",
		Role:        plugin.RoleSink,
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "host", Type: plugin.FieldTypeString, Default: "localhost", Hint: "qBittorrent host"},
			{Key: "port", Type: plugin.FieldTypeInt, Default: 8080, Hint: "qBittorrent Web UI port"},
			{Key: "username", Type: plugin.FieldTypeString, Hint: "Web UI username"},
			{Key: "password", Type: plugin.FieldTypeString, Hint: "Web UI password"},
			{Key: "savepath", Type: plugin.FieldTypePattern, Hint: "Save path template, e.g. /data/{title}"},
			{Key: "category", Type: plugin.FieldTypeString, Hint: "qBittorrent category"},
			{Key: "tags", Type: plugin.FieldTypeString, Hint: "Comma-separated tags"},
			{Key: "tls", Type: plugin.FieldTypeBool, Hint: "Use HTTPS"},
		},
	})
}

func validate(cfg map[string]any) []error {
	return plugin.OptUnknownKeys(cfg, "qbittorrent", "host", "port", "username", "password", "savepath", "category", "tags", "tls")
}

type qbtPlugin struct {
	baseURL  string
	username string
	password string
	saveIP   *interp.Interpolator
	category string
	tags     string
	client   *http.Client
	// grabStore records hash → release-URL mappings for failed-grab
	// recovery (mark_failed resolves session torrents through it).
	// nil when the plugin was constructed without a store (tests).
	grabStore *grabs.Store
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	host, _ := cfg["host"].(string)
	if host == "" {
		host = "localhost"
	}
	port := intVal(cfg["port"], 8080)

	scheme := "http"
	if tls, _ := cfg["tls"].(bool); tls {
		scheme = "https"
	}
	baseURL := fmt.Sprintf("%s://%s:%d", scheme, host, port)

	var saveIP *interp.Interpolator
	if savePat, _ := cfg["savepath"].(string); savePat != "" {
		ip, err := interp.Compile(savePat)
		if err != nil {
			return nil, fmt.Errorf("qbittorrent: invalid savepath pattern: %w", err)
		}
		saveIP = ip
	}

	jar, _ := cookiejar.New(nil)
	p := &qbtPlugin{
		baseURL:  baseURL,
		username: stringVal(cfg["username"]),
		password: stringVal(cfg["password"]),
		saveIP:   saveIP,
		category: stringVal(cfg["category"]),
		tags:     stringVal(cfg["tags"]),
		client:   &http.Client{Jar: jar},
	}
	if db != nil {
		p.grabStore = grabs.NewStore(db.Bucket(grabs.BucketName))
	}
	return p, nil
}

func (p *qbtPlugin) Name() string { return "qbittorrent" }

func (p *qbtPlugin) deliver(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	if err := p.login(ctx); err != nil {
		for _, e := range entries {
			e.Fail("qbittorrent: login failed")
		}
		return fmt.Errorf("qbittorrent: login: %w", err)
	}
	for _, e := range entries {
		savePath, err := p.renderSave(e)
		if err != nil {
			tc.Logger.Error("qbittorrent: render savepath", "title", e.Title, "err", err)
			e.Fail("qbittorrent: render savepath: " + err.Error())
			continue
		}
		if err := p.addTorrent(ctx, e.URL, savePath); err != nil {
			tc.Logger.Error("qbittorrent: add torrent", "title", e.Title, "err", err)
			e.Fail("qbittorrent: " + err.Error())
			continue
		}
		p.recordGrab(tc, e)
	}
	return nil
}

// recordGrab stores the hash → release-URL mapping so mark_failed can walk
// back from a dead session torrent to the release that produced it. The
// qBittorrent add API does not return the hash, so it comes from the entry's
// torrent_info_hash field (set by metainfo/Jackett/RSS) or the magnet URL.
// Best-effort: a bare .torrent URL with no metainfo pass has no locally
// determinable hash, which only means failed-grab recovery won't resolve
// this torrent.
func (p *qbtPlugin) recordGrab(tc *plugin.TaskContext, e *entry.Entry) {
	if p.grabStore == nil {
		return
	}
	hash := grabs.HashForEntry(e)
	if hash == "" {
		tc.Logger.Debug("qbittorrent: no info-hash for grab record", "entry", e.Title)
		return
	}
	if err := p.grabStore.Put(hash, grabs.FromEntry(e, tc.Name)); err != nil {
		tc.Logger.Warn("qbittorrent: record grab", "entry", e.Title, "err", err)
	}
}

func (p *qbtPlugin) login(ctx context.Context) error {
	form := url.Values{
		"username": {p.username},
		"password": {p.password},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/api/v2/auth/login",
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if strings.TrimSpace(string(body)) == "Fails." {
		return fmt.Errorf("authentication failed")
	}
	return nil
}

func (p *qbtPlugin) addTorrent(ctx context.Context, torrentURL, savePath string) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	w.WriteField("urls", torrentURL) //nolint:errcheck
	if savePath != "" {
		w.WriteField("savepath", savePath) //nolint:errcheck
	}
	if p.category != "" {
		w.WriteField("category", p.category) //nolint:errcheck
	}
	if p.tags != "" {
		w.WriteField("tags", p.tags) //nolint:errcheck
	}
	w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/api/v2/torrents/add",
		&buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (p *qbtPlugin) renderSave(e *entry.Entry) (string, error) {
	if p.saveIP == nil {
		return "", nil
	}
	return p.saveIP.Render(interp.EntryData(e))
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

func stringVal(v any) string {
	s, _ := v.(string)
	return s
}

func (p *qbtPlugin) Consume(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	if tc.DryRun {
		return nil
	}
	return p.deliver(ctx, tc, entries)
}
