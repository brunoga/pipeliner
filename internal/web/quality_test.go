package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunoga/pipeliner/internal/quality"
)

func newQualityTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "user", "pass")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/quality/test", srv.apiQualityTest)
	return httptest.NewServer(mux)
}

func postQuality(t *testing.T, url string, body any) (*http.Response, quality.SpecResult) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out quality.SpecResult
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return resp, out
}

func TestApiQualityTestNoMatch(t *testing.T) {
	ts := newQualityTestServer(t)
	defer ts.Close()

	resp, res := postQuality(t, ts.URL+"/api/quality/test", map[string]any{
		"title": "Show S01E01 720p WEB-DL x265",
		"spec":  "1080p+",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if res.Matched {
		t.Fatalf("expected no match")
	}
	// The resolution dimension must be present and failing.
	var found bool
	for _, d := range res.Dimensions {
		if d.Name == "resolution" {
			found = true
			if d.Passed {
				t.Errorf("resolution should fail")
			}
		}
	}
	if !found {
		t.Errorf("resolution dimension missing")
	}
}

func TestApiQualityTestMatch(t *testing.T) {
	ts := newQualityTestServer(t)
	defer ts.Close()

	resp, res := postQuality(t, ts.URL+"/api/quality/test", map[string]any{
		"title": "Show S01E01 1080p WEB h264",
		"spec":  "720p-1080p webrip+",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if !res.Matched {
		t.Fatalf("expected match, dims %+v", res.Dimensions)
	}
}

func TestApiQualityTestInvalidSpec(t *testing.T) {
	ts := newQualityTestServer(t)
	defer ts.Close()

	resp, _ := postQuality(t, ts.URL+"/api/quality/test", map[string]any{
		"title": "Show 1080p",
		"spec":  "notavalue",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid spec, got %d", resp.StatusCode)
	}
}

func TestApiQualityTestMissingTitle(t *testing.T) {
	ts := newQualityTestServer(t)
	defer ts.Close()

	resp, _ := postQuality(t, ts.URL+"/api/quality/test", map[string]any{"spec": "1080p"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing title, got %d", resp.StatusCode)
	}
}
