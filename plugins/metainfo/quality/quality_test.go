package quality

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext { return &plugin.TaskContext{Name: "test"} }

func annotate(t *testing.T, title string) *entry.Entry {
	t.Helper()
	p, _ := newPlugin(nil, nil)
	e := entry.New(title, "http://x.com/a")
	if err := p.(*qualityMetaPlugin).Annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestResolutionField(t *testing.T) {
	e := annotate(t, "Show.S01E01.1080p.BluRay.x264")
	if v := e.GetString("video_resolution"); v != "1080p" {
		t.Errorf("resolution: got %q", v)
	}
}

func TestSourceField(t *testing.T) {
	e := annotate(t, "Show.S01E01.720p.HDTV")
	if v := e.GetString("video_source"); v != "HDTV" {
		t.Errorf("source: got %q", v)
	}
}

func TestCodecField(t *testing.T) {
	e := annotate(t, "Show.S01E01.1080p.BluRay.x264")
	if v := e.GetString("codec"); v != "H264" {
		t.Errorf("codec: got %q", v)
	}
}

func TestQualityString(t *testing.T) {
	e := annotate(t, "Show.S01E01.1080p.BluRay.x264")
	if v := e.GetString("video_quality"); v == "" {
		t.Error("quality field should be set")
	}
}

func TestUnknownQualityNoFields(t *testing.T) {
	e := annotate(t, "Some.Random.Title")
	// resolution should not be set when unknown.
	if v := e.GetString("video_resolution"); v != "" {
		t.Errorf("resolution should be empty for no-quality title, got %q", v)
	}
	// quality string is still set (may be empty/unknown).
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("metainfo_quality")
	if !ok {
		t.Fatal("metainfo_quality not registered")
	}
	if d.PluginPhase != plugin.PhaseMetainfo {
		t.Errorf("phase: got %v", d.PluginPhase)
	}
}
