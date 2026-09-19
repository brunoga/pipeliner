package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestPushRequestShape: the client must hit /api/ingest/{queue} with the
// ?pipeline= trigger, the bearer token, and a {"title": ...} JSON body.
func TestPushRequestShape(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		json.NewEncoder(w).Encode(map[string]any{"queued": 1, "triggered": true}) //nolint:errcheck
	}))
	defer srv.Close()

	resp, err := push(context.Background(), srv.Client(), srv.URL, "tok123", "movies", "movies-ondemand", "Heat 1995")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/ingest/movies" {
		t.Errorf("path: %q", gotPath)
	}
	if gotQuery != "pipeline=movies-ondemand" {
		t.Errorf("query: %q", gotQuery)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("auth: %q", gotAuth)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil || body["title"] != "Heat 1995" {
		t.Errorf("body: %q", gotBody)
	}
	if resp.Queued != 1 || !resp.Triggered {
		t.Errorf("response: %+v", resp)
	}
}

func TestPushServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()
	if _, err := push(context.Background(), srv.Client(), srv.URL, "bad", "movies", "p", "x"); err == nil {
		t.Fatal("401 must surface as an error")
	}
}

// TestResolveToken: flag beats env beats client.conf beats the token file;
// missing everything errors.
func TestResolveToken(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "ingest-token")
	if err := os.WriteFile(file, []byte("  file-tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if tok, src, err := resolveToken("flag-tok", "env-tok", "conf-tok", file); err != nil || tok != "flag-tok" || src != "flag" {
		t.Errorf("flag precedence: %q %q %v", tok, src, err)
	}
	if tok, src, err := resolveToken("", "env-tok", "conf-tok", file); err != nil || tok != "env-tok" || src == "" {
		t.Errorf("env precedence: %q %q %v", tok, src, err)
	}
	if tok, src, err := resolveToken("", "", "conf-tok", file); err != nil || tok != "conf-tok" || src == "" {
		t.Errorf("conf precedence: %q %q %v", tok, src, err)
	}
	if tok, _, err := resolveToken("", "", "", file); err != nil || tok != "file-tok" {
		t.Errorf("file fallback (trimmed): %q %v", tok, err)
	}
	if _, _, err := resolveToken("", "", "", filepath.Join(dir, "missing")); err == nil {
		t.Error("no source anywhere must error")
	}
}

func TestResolveURL(t *testing.T) {
	if got := resolveURL("https://x.example/", "", ""); got != "https://x.example" {
		t.Errorf("flag with trailing slash: %q", got)
	}
	if got := resolveURL("", "https://env.example", "https://conf.example"); got != "https://env.example" {
		t.Errorf("env beats conf: %q", got)
	}
	if got := resolveURL("", "", "https://conf.example/"); got != "https://conf.example" {
		t.Errorf("conf: %q", got)
	}
	if got := resolveURL("", "", ""); got != "http://localhost:8080" {
		t.Errorf("default: %q", got)
	}
}

// TestLoadClientConf: key=value lines with comments and junk tolerated;
// missing file is an empty conf.
func TestLoadClientConf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client.conf")
	content := "# pipeliner client config\nurl = https://pipeliner.example.com/\n\nTOKEN=  abc123  \nnot a kv line\nignored_key = x\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	conf := loadClientConf(path)
	if conf["url"] != "https://pipeliner.example.com/" {
		t.Errorf("url: %q", conf["url"])
	}
	if conf["token"] != "abc123" {
		t.Errorf("token (lowercased key, trimmed value): %q", conf["token"])
	}
	if len(loadClientConf(filepath.Join(dir, "missing"))) != 0 {
		t.Error("missing file must be an empty conf")
	}
}
