package integration

import (
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/config"

	_ "github.com/brunoga/pipeliner/plugins/processor/filter/limit"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/route"
)

// TestCatchAllRouteBelowDedupDoesNotWarn: a route whose last port always
// matches never refuses an entry, so it can split what dedup kept, for
// example to cap each kind separately, without losing an item. A route with no
// catch-all port can still reject an unmatched release, so below dedup it
// keeps the warning.
func TestCatchAllRouteBelowDedupDoesNotWarn(t *testing.T) {
	check := func(t *testing.T, ports string) string {
		t.Helper()
		cfg, err := config.ParseBytes([]byte(`
src  = input("rss", url="https://example.com/rss")
meta = process("metainfo_file", upstream=src)
acc  = process("accept_all", upstream=meta)
dd   = process("dedup", upstream=acc)
r    = route(dd, ` + ports + `)
big  = process("limit", upstream=r.big, n=1)
rest = process("limit", upstream=r.rest, n=2)
output("print", upstream=merge(big, rest))
pipeline("t")
`))
		if err != nil {
			t.Fatal(err)
		}
		errs, warnings := config.Validate(cfg)
		if len(errs) > 0 {
			t.Fatalf("errors: %v", errs)
		}
		var b strings.Builder
		for _, w := range warnings {
			b.WriteString(w.Error() + "\n")
		}
		return b.String()
	}

	if got := check(t, `big='video_resolution == "2160p"', rest="true"`); strings.Contains(got, "keeps only one release per item") {
		t.Errorf("a catch-all route cannot lose an item, but it warned:\n%s", got)
	}
	if got := check(t, `big='video_resolution == "2160p"', rest='video_resolution == "1080p"'`); !strings.Contains(got, "keeps only one release per item") {
		t.Errorf("a route without a catch-all can reject an unmatched release below dedup and should warn, got:\n%s", got)
	}
}
