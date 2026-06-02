package jackett

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/brunoga/pipeliner/internal/plugin"
)

// indexerCaps describes what an indexer supports, as reported by its
// Torznab t=caps endpoint. It drives query construction so we only send
// search modes and typed parameters the indexer actually accepts —
// 3dtorrents, for example, advertises t=search only and rejects t=movie
// with Torznab error 201.
type indexerCaps struct {
	search      modeCaps // t=search   (generic free-text)
	movieSearch modeCaps // t=movie
	tvSearch    modeCaps // t=tvsearch
}

// modeCaps describes one Torznab search mode.
type modeCaps struct {
	available bool            // <search available="yes"/>
	params    map[string]bool // parsed supportedParams="q,year,imdbid,..."; nil means "unknown — accept everything"
}

// supports reports whether this mode allows the named parameter. If the
// indexer didn't enumerate supportedParams, we conservatively assume the
// param is allowed (caller may still be rejected by the indexer, in which
// case the 201 fallback in searchIndexer kicks in).
func (m modeCaps) supports(param string) bool {
	if m.params == nil {
		return true
	}
	return m.params[param]
}

type capsResponse struct {
	XMLName   xml.Name `xml:"caps"`
	Searching struct {
		Search      capsModeNode `xml:"search"`
		TVSearch    capsModeNode `xml:"tv-search"`
		MovieSearch capsModeNode `xml:"movie-search"`
	} `xml:"searching"`
}

type capsModeNode struct {
	Available       string `xml:"available,attr"`
	SupportedParams string `xml:"supportedParams,attr"`
}

// parseCaps parses a Torznab <caps> XML response. Returns an error if the
// payload is not a valid caps document so the caller can treat the indexer
// as "caps unknown" and fall back to the generic-then-201 path.
func parseCaps(data []byte) (*indexerCaps, error) {
	var resp capsResponse
	if err := xml.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse caps: %w", err)
	}
	if resp.XMLName.Local != "caps" {
		return nil, fmt.Errorf("parse caps: unexpected root element %q", resp.XMLName.Local)
	}
	return &indexerCaps{
		search:      toModeCaps(resp.Searching.Search),
		movieSearch: toModeCaps(resp.Searching.MovieSearch),
		tvSearch:    toModeCaps(resp.Searching.TVSearch),
	}, nil
}

func toModeCaps(n capsModeNode) modeCaps {
	m := modeCaps{available: strings.EqualFold(n.Available, "yes")}
	if s := strings.TrimSpace(n.SupportedParams); s != "" {
		m.params = make(map[string]bool)
		for p := range strings.SplitSeq(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				m.params[p] = true
			}
		}
	}
	return m
}

// getCaps returns cached caps for indexer, fetching them on first use.
// On fetch failure returns nil — callers treat that as "caps unknown" and
// fall back to the typed-query-with-201-retry path. We do not cache
// failures so a transient fetch error doesn't permanently disable typed
// queries for an indexer.
func (p *jackettPlugin) getCaps(ctx context.Context, tc *plugin.TaskContext, indexer string) *indexerCaps {
	p.capsMu.Lock()
	c, ok := p.capsCache[indexer]
	p.capsMu.Unlock()
	if ok {
		return c
	}

	c, err := p.fetchCaps(ctx, indexer)
	if err != nil {
		tc.Logger.Warn("jackett: caps fetch failed, falling back to typed-then-201 path",
			"indexer", indexer, "err", err)
		return nil
	}

	p.capsMu.Lock()
	p.capsCache[indexer] = c
	p.capsMu.Unlock()
	return c
}

func (p *jackettPlugin) fetchCaps(ctx context.Context, indexer string) (*indexerCaps, error) {
	endpoint := fmt.Sprintf("%s/api/v2.0/indexers/%s/results/torznab/api",
		p.baseURL, url.PathEscape(indexer))
	params := url.Values{
		"t":      {"caps"},
		"apikey": {p.apiKey},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "pipeliner/1.0")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := checkTorznabError(body); err != nil {
		return nil, err
	}
	return parseCaps(body)
}
