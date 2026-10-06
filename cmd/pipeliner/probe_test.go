package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	probecmd "github.com/brunoga/pipeliner/plugins/processor/probe"
)

func TestCmdProbeUsage(t *testing.T) {
	// No argument, and more than one, are both usage errors rather than a
	// probe of something unintended.
	for _, args := range [][]string{{}, {"a", "b"}} {
		if code := cmdProbe(args); code != 1 {
			t.Errorf("cmdProbe(%v) = %d, want 1", args, code)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	for in, want := range map[int64]string{
		0:        "0 B",
		512:      "512 B",
		1024:     "1.0 KiB",
		10 << 20: "10.0 MiB",
		1536:     "1.5 KiB",
		25165824: "24.0 MiB",
		3 << 30:  "3.0 GiB",
	} {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

// A probe that cannot even fetch the .torrent must fail rather than report an
// empty verdict — "could not tell" and "it is not a disc" are different
// answers, and the exit code keeps them apart.
func TestInspectReportsFetchFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rep, err := probecmd.Inspect(ctx, nil, srv.URL+"/x.torrent", 5*time.Second, 6)
	if err == nil {
		t.Fatalf("want an error for an unfetchable .torrent, got %+v", rep)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error %q should say what the server answered", err)
	}
}

// A path that ends in .torrent but holds nonsense must fail at parsing rather
// than be treated as a URL.
func TestInspectRejectsAMalformedTorrentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.torrent")
	if err := os.WriteFile(path, []byte("not bencode"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := probecmd.Inspect(ctx, nil, path, 5*time.Second, 6); err == nil {
		t.Fatal("a malformed .torrent must not probe successfully")
	}
}

// A magnet has no .torrent for a tracker to serve, which the probe says
// plainly rather than hanging on metadata exchange.
func TestInspectRejectsAMagnet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := probecmd.Inspect(ctx, nil, "magnet:?xt=urn:btih:"+strings.Repeat("a", 40), 5*time.Second, 6)
	if err == nil {
		t.Fatal("want an error for a magnet")
	}
}
