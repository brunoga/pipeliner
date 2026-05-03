// Package download streams files from entry URLs to a local directory.
package download

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"text/template"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	itpl "github.com/brunoga/pipeliner/internal/template"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "download",
		Description: "download entry URLs to a local directory",
		PluginPhase: plugin.PhaseOutput,
		Factory:     newPlugin,
	})
}

type downloadPlugin struct {
	dir      string
	filenameTmpl *template.Template
	client   *http.Client
}

func newPlugin(cfg map[string]any) (plugin.Plugin, error) {
	dir, _ := cfg["path"].(string)
	if dir == "" {
		return nil, fmt.Errorf("download: 'path' is required")
	}

	filenamePat, _ := cfg["filename"].(string)
	if filenamePat == "" {
		filenamePat = "{{.url_basename}}"
	}
	tmpl, err := template.New("filename").Funcs(itpl.FuncMap()).Parse(filenamePat)
	if err != nil {
		return nil, fmt.Errorf("download: invalid filename template: %w", err)
	}

	return &downloadPlugin{
		dir:          dir,
		filenameTmpl: tmpl,
		client:       &http.Client{Timeout: 0}, // no timeout — large files
	}, nil
}

func (p *downloadPlugin) Name() string        { return "download" }
func (p *downloadPlugin) Phase() plugin.Phase { return plugin.PhaseOutput }

func (p *downloadPlugin) Output(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return fmt.Errorf("download: create dir %s: %w", p.dir, err)
	}
	for _, e := range entries {
		if err := p.downloadEntry(ctx, tc, e); err != nil {
			tc.Logger.Error("download failed", "title", e.Title, "err", err)
		}
	}
	return nil
}

func (p *downloadPlugin) downloadEntry(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	data := entryToMap(e)

	filename, err := renderTemplate(p.filenameTmpl, data)
	if err != nil {
		return fmt.Errorf("render filename: %w", err)
	}
	if filename == "" {
		return fmt.Errorf("filename is empty for %q", e.URL)
	}

	dest := filepath.Join(p.dir, filepath.Base(filename))
	tmp := dest + ".part"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "pipeliner/1.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", e.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, e.URL)
	}

	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("write %s: %w", tmp, copyErr)
	}
	if closeErr != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("close %s: %w", tmp, closeErr)
	}

	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("rename to %s: %w", dest, err)
	}

	tc.Logger.Info("downloaded", "title", e.Title, "path", dest)
	e.Set("download_path", dest)
	return nil
}

func renderTemplate(tmpl *template.Template, data map[string]any) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func entryToMap(e *entry.Entry) map[string]any {
	m := map[string]any{
		"Title":        e.Title,
		"URL":          e.URL,
		"OriginalURL":  e.OriginalURL,
		"Task":         e.Task,
		"url_basename": urlBasename(e.URL),
		"timestamp":    time.Now().Format("20060102-150405"),
	}
	maps.Copy(m, e.Fields)
	return m
}

func urlBasename(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "download"
	}
	base := filepath.Base(u.Path)
	if base == "." || base == "/" {
		return "download"
	}
	return base
}
