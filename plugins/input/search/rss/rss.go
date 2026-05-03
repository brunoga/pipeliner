// Package rss provides a search plugin that fetches entries from a
// parameterized RSS URL. The URL is constructed by rendering a pattern
// with the search query substituted for {Query} or {QueryEscaped}.
//
// This plugin is used as a sub-plugin of the discover input plugin via the
// "via" config key. It cannot be used directly as a task-level plugin.
//
// Config keys:
//
//	url_template - Pattern string for the search URL (required).
//	               Use {Query} for the raw query or {QueryEscaped} for URL-encoded.
//	               Go template syntax ({{.Query}}) is also accepted.
//	               Example: "https://jackett.example.com/api/v2.0/indexers/all/results/torznab?q={QueryEscaped}"
package rss

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/interp"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "search_rss",
		Description: "search a parameterized RSS URL for entries matching a query string",
		PluginPhase: plugin.PhaseSearch,
		Factory:     newPlugin,
	})
}

type searchRSSPlugin struct {
	urlIP  *interp.Interpolator
	client *http.Client
}

func newPlugin(cfg map[string]any) (plugin.Plugin, error) {
	urlTemplate, _ := cfg["url_template"].(string)
	if urlTemplate == "" {
		return nil, fmt.Errorf("search_rss: 'url_template' is required")
	}
	ip, err := interp.Compile(urlTemplate)
	if err != nil {
		return nil, fmt.Errorf("search_rss: invalid url_template: %w", err)
	}
	return &searchRSSPlugin{
		urlIP:  ip,
		client: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (p *searchRSSPlugin) Name() string        { return "search_rss" }
func (p *searchRSSPlugin) Phase() plugin.Phase { return plugin.PhaseSearch }

func (p *searchRSSPlugin) Search(ctx context.Context, _ *plugin.TaskContext, query string) ([]*entry.Entry, error) {
	data := map[string]any{
		"Query":        query,
		"QueryEscaped": url.QueryEscape(query),
	}
	fetchURL, err := p.urlIP.Render(data)
	if err != nil {
		return nil, fmt.Errorf("search_rss: render URL for %q: %w", query, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("search_rss: build request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search_rss: fetch %q: %w", fetchURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("search_rss: read body: %w", err)
	}

	entries, err := parseRSS(body)
	if err != nil {
		return nil, fmt.Errorf("search_rss: parse feed: %w", err)
	}
	return entries, nil
}

type rss20Feed struct {
	Channel struct {
		Items []rss20Item `xml:"item"`
	} `xml:"channel"`
}

type rss20Item struct {
	Title     string `xml:"title"`
	Link      string `xml:"link"`
	GUID      string `xml:"guid"`
	Enclosure struct {
		URL  string `xml:"url,attr"`
		Type string `xml:"type,attr"`
	} `xml:"enclosure"`
}

func parseRSS(data []byte) ([]*entry.Entry, error) {
	var feed rss20Feed
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, err
	}
	var entries []*entry.Entry
	for _, item := range feed.Channel.Items {
		link := item.Link
		if link == "" {
			link = item.GUID
		}
		if link == "" && item.Enclosure.URL != "" {
			link = item.Enclosure.URL
		}
		if link == "" {
			continue
		}
		title := strings.TrimSpace(item.Title)
		if title == "" {
			title = link
		}
		entries = append(entries, entry.New(title, link))
	}
	return entries, nil
}
