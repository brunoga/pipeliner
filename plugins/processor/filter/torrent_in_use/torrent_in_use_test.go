package torrent_in_use

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/torrentclient"
)

type fakeClient struct {
	torrents []torrentclient.Torrent
	err      error
}

func (f *fakeClient) ListTorrents(context.Context) ([]torrentclient.Torrent, error) {
	return f.torrents, f.err
}
func (f *fakeClient) Remove(context.Context, []string, bool) error { return nil }
func (f *fakeClient) Pause(context.Context, []string) error        { return nil }
func (f *fakeClient) Reannounce(context.Context, []string) error   { return nil }

func tc() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "t", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func file(path string) *entry.Entry {
	e := entry.New(path, "file://"+path)
	e.Set(entry.FieldFileLocation, path)
	return e
}

// TestFilesATorrentUsesAreKept: the files of a folder torrent and of a
// single-file torrent are refused; their neighbours, and a file whose name
// merely starts like a torrent's folder, are not.
func TestFilesATorrentUsesAreKept(t *testing.T) {
	p := &inUsePlugin{client: &fakeClient{torrents: []torrentclient.Torrent{
		{Name: "RIO_2_3D", DownloadDir: "/data/media/3dmovies/Rio 2 (2014)"},
		{Name: "Show.S01E01.mkv", DownloadDir: "/data/media/series/Show/Season 01"},
	}}}
	inFolder := file("/data/media/3dmovies/Rio 2 (2014)/RIO_2_3D/BDMV/STREAM/00001.m2ts")
	single := file("/data/media/series/Show/Season 01/Show.S01E01.mkv")
	sibling := file("/data/media/series/Show/Season 01/Show.S01E02.mkv")
	lookalike := file("/data/media/3dmovies/Rio 2 (2014)/RIO_2_3D.nfo")
	if _, err := p.Process(context.Background(), tc(), []*entry.Entry{inFolder, single, sibling, lookalike}); err != nil {
		t.Fatal(err)
	}
	if !inFolder.IsRejected() || !single.IsRejected() {
		t.Errorf("files in use must be kept: folder %v single %v", inFolder.IsRejected(), single.IsRejected())
	}
	if sibling.IsRejected() || lookalike.IsRejected() {
		t.Errorf("files no torrent uses must pass: sibling %q lookalike %q", sibling.RejectReason, lookalike.RejectReason)
	}
}

// TestAnUnreachableClientKeepsEverything: not knowing which files are in use
// must never read as "none are".
func TestAnUnreachableClientKeepsEverything(t *testing.T) {
	p := &inUsePlugin{client: &fakeClient{err: errors.New("connection refused")}}
	e := file("/data/media/movies/x.mkv")
	if _, err := p.Process(context.Background(), tc(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if !e.IsRejected() {
		t.Error("with the client unreachable, every file must be kept")
	}
}

func TestValidate(t *testing.T) {
	if errs := validate(map[string]any{}); len(errs) == 0 {
		t.Error("backend is required")
	}
	if errs := validate(map[string]any{"backend": "deluge", "bogus": 1}); len(errs) == 0 {
		t.Error("unknown keys are refused")
	}
	if errs := validate(map[string]any{"backend": "deluge", "host": "h", "password": "p"}); len(errs) != 0 {
		t.Errorf("valid config refused: %v", errs)
	}
}
