// Package html provides an HTML link-extraction input plugin.
package html

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "html",
		Description: "extract links from an HTML page",
		Role:        plugin.RoleSource,
		Produces:    []string{"html_page"},
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "url", "html"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "html", "url", "mask")...)
	return errs
}

type htmlPlugin struct {
	pageURL string
	mask    string // glob pattern to filter hrefs; empty = accept all
	client  *http.Client
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	u, _ := cfg["url"].(string)
	if u == "" {
		return nil, fmt.Errorf("html: 'url' is required")
	}
	mask, _ := cfg["mask"].(string)
	return &htmlPlugin{
		pageURL: u,
		mask:    mask,
		client:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (p *htmlPlugin) Name() string        { return "html" }

func (p *htmlPlugin) Generate(ctx context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("html: build request: %w", err)
	}
	req.Header.Set("User-Agent", "pipeliner/1.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("html: fetch %s: %w", p.pageURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("html: HTTP %d from %s", resp.StatusCode, p.pageURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("html: read body: %w", err)
	}

	links := extractLinks(string(body))

	base, _ := url.Parse(p.pageURL)
	var entries []*entry.Entry
	for _, lk := range links {
		href := resolveURL(base, lk.href)
		if href == "" {
			continue
		}
		if p.mask != "" {
			// Match the mask against the URL's last path segment so that
			// patterns like "*.torrent" work without requiring the full URL.
			segment := filepath.Base(href)
			matched, err := filepath.Match(p.mask, segment)
			if err != nil || !matched {
				continue
			}
		}
		title := strings.TrimSpace(lk.text)
		if title == "" {
			title = href
		}
		e := entry.New(title, href)
		e.Set("html_page", p.pageURL)
		entries = append(entries, e)
	}
	return entries, nil
}

// link holds the extracted href and inner text of an <a> element.
type link struct {
	href string
	text string
}

// extractLinks performs a minimal state-machine scan over raw HTML to find
// all <a href="...">text</a> elements. It handles single and double quotes,
// case-insensitive tag names, and arbitrary whitespace in attributes.
func extractLinks(html string) []link {
	var links []link
	s := html
	for {
		// Find next opening tag.
		start := indexFold(s, "<a")
		if start < 0 {
			break
		}
		rest := s[start+2:] // skip "<a"

		// The character after "a" must be whitespace or ">" to avoid matching
		// <abbr>, <article>, etc.
		if len(rest) == 0 || (!isSpace(rest[0]) && rest[0] != '>') {
			s = s[start+2:]
			continue
		}

		// Find end of opening tag.
		attrs, after, ok2 := strings.Cut(rest, ">")
		if !ok2 {
			break
		}
		href := extractAttr(attrs, "href")

		// Collect text content until </a>.
		closeIdx := indexFold(after, "</a>")
		var text string
		if closeIdx >= 0 {
			text = stripTags(after[:closeIdx])
			s = after[closeIdx+4:]
		} else {
			s = after
		}

		if href != "" {
			links = append(links, link{href: href, text: text})
		}
	}
	return links
}

// extractAttr extracts the value of a named attribute from an attribute string.
func extractAttr(attrs, name string) string {
	lower := strings.ToLower(attrs)
	needle := name + "="
	_, after, ok := strings.Cut(lower, needle)
	if !ok {
		return ""
	}
	// Offset back to original-case attrs using the cut position.
	rest := attrs[len(attrs)-len(after):]
	if len(rest) == 0 {
		return ""
	}
	if rest[0] == '"' || rest[0] == '\'' {
		quote := rest[0]
		end := strings.IndexByte(rest[1:], quote)
		if end < 0 {
			return strings.TrimSpace(rest[1:])
		}
		return rest[1 : end+1]
	}
	// Unquoted value ends at whitespace or '>'.
	end := strings.IndexAny(rest, " \t\r\n>")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// stripTags removes any HTML tags from s, returning plain text.
func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, ch := range s {
		switch {
		case ch == '<':
			inTag = true
		case ch == '>':
			inTag = false
		case !inTag:
			b.WriteRune(ch)
		}
	}
	return b.String()
}

// indexFold returns the index of the first case-insensitive occurrence of
// substr in s, or -1.
func indexFold(s, substr string) int {
	lower := strings.ToLower(s)
	return strings.Index(lower, strings.ToLower(substr))
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

// resolveURL resolves href relative to base, returning the absolute URL string.
func resolveURL(base *url.URL, href string) string {
	if base == nil {
		return href
	}
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
}
