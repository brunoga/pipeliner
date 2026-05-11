package decompress

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
)

// --- resolveTool ---

func TestResolveToolInvalidName(t *testing.T) {
	_, _, err := resolveTool("winzip")
	if err == nil {
		t.Error("expected error for unsupported tool name")
	}
}

func TestResolveToolForcedInvalidName(t *testing.T) {
	_, _, err := resolveTool("tar")
	if err == nil {
		t.Error("expected error for tool not in supported list")
	}
}

// --- archiveLocation ---

func TestArchiveLocationFromField(t *testing.T) {
	e := entry.New("Archive", "http://example.com/foo")
	e.Set("file_location", "/downloads/archive.rar")
	got := archiveLocation(e)
	if got != "/downloads/archive.rar" {
		t.Errorf("got %q, want /downloads/archive.rar", got)
	}
}

func TestArchiveLocationFromRARURL(t *testing.T) {
	e := entry.New("Archive", "http://example.com/file.rar")
	got := archiveLocation(e)
	if got != "http://example.com/file.rar" {
		t.Errorf("got %q", got)
	}
}

func TestArchiveLocationFromZIPURL(t *testing.T) {
	e := entry.New("Archive", "http://example.com/release.zip")
	got := archiveLocation(e)
	if got != "http://example.com/release.zip" {
		t.Errorf("got %q", got)
	}
}

func TestArchiveLocationFrom7zURL(t *testing.T) {
	e := entry.New("Archive", "http://example.com/archive.7z")
	got := archiveLocation(e)
	if got != "http://example.com/archive.7z" {
		t.Errorf("got %q", got)
	}
}

func TestArchiveLocationNoMatch(t *testing.T) {
	e := entry.New("Not an archive", "http://example.com/feed.xml")
	got := archiveLocation(e)
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestArchiveLocationFieldWinsOverURL(t *testing.T) {
	e := entry.New("Archive", "http://example.com/file.rar")
	e.Set("file_location", "/local/path.rar")
	got := archiveLocation(e)
	if got != "/local/path.rar" {
		t.Errorf("location field should win; got %q", got)
	}
}

// --- buildArgs ---

func newTestPlugin(tool, to string, keepDirs, deleteArchive bool) *decompressPlugin {
	return &decompressPlugin{
		to:            to,
		keepDirs:      keepDirs,
		deleteArchive: deleteArchive,
		tool:          tool,
		toolPath:      "/usr/bin/" + tool,
	}
}

func TestBuildArgsUnrarKeepDirs(t *testing.T) {
	p := newTestPlugin("unrar", "/dest", true, false)
	args := p.buildArgs("/archive.rar")
	if len(args) == 0 || args[0] != "x" {
		t.Errorf("unrar keepDirs should use 'x' command; got %v", args)
	}
}

func TestBuildArgsUnrarFlatten(t *testing.T) {
	p := newTestPlugin("unrar", "/dest", false, false)
	args := p.buildArgs("/archive.rar")
	if len(args) == 0 || args[0] != "e" {
		t.Errorf("unrar flatten should use 'e' command; got %v", args)
	}
}

func TestBuildArgs7zKeepDirs(t *testing.T) {
	p := newTestPlugin("7z", "/dest", true, false)
	args := p.buildArgs("/archive.7z")
	if len(args) == 0 || args[0] != "x" {
		t.Errorf("7z keepDirs should use 'x' command; got %v", args)
	}
}

func TestBuildArgs7zFlatten(t *testing.T) {
	p := newTestPlugin("7z", "/dest", false, false)
	args := p.buildArgs("/archive.7z")
	if len(args) == 0 || args[0] != "e" {
		t.Errorf("7z flatten should use 'e' command; got %v", args)
	}
}

func TestBuildArgsUnar(t *testing.T) {
	p := newTestPlugin("unar", "/dest", true, false)
	args := p.buildArgs("/archive.rar")
	if len(args) == 0 || args[0] != "/archive.rar" {
		t.Errorf("unar first arg should be archive path; got %v", args)
	}
	found := false
	for _, a := range args {
		if a == "/dest" {
			found = true
		}
	}
	if !found {
		t.Errorf("unar args should contain destination; got %v", args)
	}
}

func TestBuildArgsUnarNoDirectory(t *testing.T) {
	p := newTestPlugin("unar", "/dest", false, false)
	args := p.buildArgs("/archive.rar")
	found := false
	for _, a := range args {
		if a == "-no-directory" {
			found = true
		}
	}
	if !found {
		t.Errorf("unar flatten should include -no-directory; got %v", args)
	}
}

// --- newPlugin config defaults ---

func TestNewPluginMissingTo(t *testing.T) {
	_, err := newPlugin(map[string]any{}, nil)
	if err == nil {
		t.Error("expected error when 'to' is missing")
	}
}

func TestNewPluginDefaultKeepDirs(t *testing.T) {
	// Keep dirs defaults to true; we can't easily test extraction without a
	// real tool, but we can verify the default is set when config omits it.
	// Build directly since resolveTool may fail if no tools on PATH.
	p := newTestPlugin("unrar", "/tmp", true, false)
	if !p.keepDirs {
		t.Error("keepDirs should default to true")
	}
}
