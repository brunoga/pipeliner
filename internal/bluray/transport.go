package bluray

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// Transport is a pluggable HTTP/TLS execution layer used by Client.
// By default, Client uses StandardTransport (Go net/http with coherent Chrome
// browser headers, Client Hints, Fetch Metadata, and a CookieJar).
type Transport interface {
	Do(req *http.Request) (*http.Response, error)
	SetCookies(u *url.URL, cookies []*http.Cookie)
	Cookies(u *url.URL) []*http.Cookie
}

// BrowserProfile defines the User-Agent and Sec-CH-UA client hint headers for browser simulation.
type BrowserProfile struct {
	UserAgent       string
	SecChUA         string
	SecChUAMobile   string
	SecChUAPlatform string
	AcceptLanguage  string
}

// DefaultChromeProfile returns a coherent Chrome 131 on Linux header profile.
func DefaultChromeProfile() BrowserProfile {
	return BrowserProfile{
		UserAgent:       "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		SecChUA:         `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		SecChUAMobile:   "?0",
		SecChUAPlatform: `"Linux"`,
		AcceptLanguage:  "en-US,en;q=0.9",
	}
}

// ApplyBrowserHeaders populates a request with coherent Chrome headers.
// If isAJAX is true, it sets XHR/fetch metadata headers instead of document navigation headers.
func ApplyBrowserHeaders(req *http.Request, profile BrowserProfile, referer string, isAJAX bool) {
	req.Header.Set("User-Agent", profile.UserAgent)
	req.Header.Set("Accept-Language", profile.AcceptLanguage)
	if req.Header.Get("Accept-Encoding") == "" {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	req.Header.Set("Sec-Ch-Ua", profile.SecChUA)
	req.Header.Set("Sec-Ch-Ua-Mobile", profile.SecChUAMobile)
	req.Header.Set("Sec-Ch-Ua-Platform", profile.SecChUAPlatform)

	if isAJAX {
		req.Header.Set("Accept", "*/*")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("Sec-Fetch-Dest", "empty")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
	} else {
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
		req.Header.Set("Cache-Control", "no-cache")
		req.Header.Set("Pragma", "no-cache")
		req.Header.Set("Sec-Fetch-Dest", "document")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		if referer != "" {
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set("Referer", referer)
		} else {
			req.Header.Set("Sec-Fetch-Site", "none")
		}
		req.Header.Set("Sec-Fetch-User", "?1")
		req.Header.Set("Upgrade-Insecure-Requests", "1")
	}
}

// StandardTransport implements Transport using the Go standard library net/http.Client
// with automatic CookieJar management and transparent gzip decompression.
type StandardTransport struct {
	client *http.Client
	jar    http.CookieJar
}

// NewStandardTransport creates a StandardTransport with the given timeout.
func NewStandardTransport(timeout time.Duration) (*StandardTransport, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &StandardTransport{
		jar: jar,
		client: &http.Client{
			Jar:     jar,
			Timeout: timeout,
		},
	}, nil
}

// Do executes the HTTP request and transparently decompresses gzip responses if needed.
func (t *StandardTransport) Do(req *http.Request) (*http.Response, error) {
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		resp.Body = &gzipReadCloser{
			Reader: gzReader,
			raw:    resp.Body,
		}
		resp.Header.Del("Content-Encoding")
	}
	return resp, nil
}

// SetCookies sets cookies for the given URL in the underlying cookie jar.
func (t *StandardTransport) SetCookies(u *url.URL, cookies []*http.Cookie) {
	t.jar.SetCookies(u, cookies)
}

// Cookies returns the cookies for the given URL from the underlying cookie jar.
func (t *StandardTransport) Cookies(u *url.URL) []*http.Cookie {
	return t.jar.Cookies(u)
}

type gzipReadCloser struct {
	io.Reader
	raw io.ReadCloser
}

func (g *gzipReadCloser) Close() error {
	if c, ok := g.Reader.(io.Closer); ok {
		_ = c.Close()
	}
	return g.raw.Close()
}

// RateLimiter paces outgoing requests with a minimum interval and random jitter.
type RateLimiter interface {
	Wait(ctx context.Context) error
}
