package series

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
	if err := p.(*seriesMetaPlugin).Annotate(context.Background(), makeCtx(), e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestStandardEpisode(t *testing.T) {
	e := annotate(t, "My.Show.S02E05.720p.HDTV")
	if v := e.GetString("title"); v != "My Show" {
		t.Errorf("title: got %q", v)
	}
	if v := e.GetString("series_episode_id"); v != "S02E05" {
		t.Errorf("episode_id: got %q", v)
	}
	if v := e.GetInt("series_season"); v != 2 {
		t.Errorf("series_season: got %d", v)
	}
	if v := e.GetInt("series_episode"); v != 5 {
		t.Errorf("series_episode: got %d", v)
	}
}

func TestDoubleEpisode(t *testing.T) {
	e := annotate(t, "My.Show.S01E01E02.720p.HDTV")
	if v := e.GetInt("series_double_episode"); v != 2 {
		t.Errorf("series_double_episode: got %d", v)
	}
}

func TestProperFlag(t *testing.T) {
	e := annotate(t, "My.Show.S01E01.PROPER.720p.HDTV")
	if v, _ := e.Get("series_proper"); v != true {
		t.Errorf("series_proper: got %v", v)
	}
}

func TestServiceField(t *testing.T) {
	e := annotate(t, "My.Show.S01E01.NF.1080p.WEB-DL")
	if v := e.GetString("series_service"); v != "Netflix" {
		t.Errorf("series_service: got %q", v)
	}
}

func TestContainerField(t *testing.T) {
	e := annotate(t, "My.Show.S01E01.1080p.BluRay.mkv")
	if v := e.GetString("series_container"); v != "mkv" {
		t.Errorf("series_container: got %q", v)
	}
}

func TestNoMatchLeavesSilent(t *testing.T) {
	e := annotate(t, "Just A Random Movie 2023 1080p")
	if v := e.GetString("title"); v != "" {
		t.Errorf("non-episode title should not set title, got %q", v)
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("metainfo_series")
	if !ok {
		t.Fatal("metainfo_series not registered")
	}
	if d.Role != plugin.RoleProcessor {
		t.Errorf("phase: got %v", d.Role)
	}
}
