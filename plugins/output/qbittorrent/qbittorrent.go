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
	"github.com/brunoga/pipeliner/internal/interp"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "qbittorrent",
		Description: "add torrents to a qBittorrent daemon via Web API v2",
		PluginPhase: plugin.PhaseOutput,
		Factory:     newPlugin,
		Validate:    validate,
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
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
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

	savePat, _ := cfg["savepath"].(string)
	if savePat == "" {
		savePat = "{download_path}"
	}
	saveIP, err := interp.Compile(savePat)
	if err != nil {
		return nil, fmt.Errorf("qbittorrent: invalid savepath pattern: %w", err)
	}

	jar, _ := cookiejar.New(nil)
	return &qbtPlugin{
		baseURL:  baseURL,
		username: stringVal(cfg["username"]),
		password: stringVal(cfg["password"]),
		saveIP:   saveIP,
		category: stringVal(cfg["category"]),
		tags:     stringVal(cfg["tags"]),
		client:   &http.Client{Jar: jar},
	}, nil
}

func (p *qbtPlugin) Name() string        { return "qbittorrent" }
func (p *qbtPlugin) Phase() plugin.Phase { return plugin.PhaseOutput }

func (p *qbtPlugin) Output(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
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
		}
	}
	return nil
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
	body, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(body)) == "Fails." {
		return fmt.Errorf("authentication failed")
	}
	return nil
}

func (p *qbtPlugin) addTorrent(ctx context.Context, torrentURL, savePath string) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	w.WriteField("urls", torrentURL)   //nolint:errcheck
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
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (p *qbtPlugin) renderSave(e *entry.Entry) (string, error) {
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
