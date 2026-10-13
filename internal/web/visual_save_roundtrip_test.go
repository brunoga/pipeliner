package web_test

import (
	"strings"
	"testing"
	"time"
)

// roundTripConfig reproduces, in miniature, the three things one plain visual
// save of the live config lost:
//
//   - a section header separated from the first node by a blank line;
//   - the comment above a user-function call;
//   - a node's own literal, after a deletion renumbered the loader's IDs so
//     that this node's ID is the NAME of its neighbour's variable. rss_0 is
//     not in the file, so the loader assigns rss_0 to variable rss_1 and
//     rss_1 to variable rss_2 — and variable rss_1 holds url=FEED.
const roundTripConfig = `FEED = "https://example.com/shared"

# pipeliner:param path
def tail_fn(upstream, path):
    t = process("pathfmt", upstream=upstream, field="move_to_path", path=path)
    return t

# ── tv: a section header, two lines long,
# separated from the node below by a blank line

# The first feed, by reference.
rss_1 = input("rss", url=FEED)

# The second feed, with a literal url of its own.
rss_2 = input("rss", url="https://example.com/literal")

seen_9 = process("seen", upstream=rss_2)

# A comment above a function call.
out = tail_fn(upstream=seen_9, path="/media/{title}")

shown = output("print", upstream=out)
also = output("print", upstream=rss_1)
pipeline("tv")`

// TestE2EVisualSaveRoundTripLosesNothing does what the user did — load a
// config, let the visual editor regenerate the text, and look at what Save &
// Reload would send — and checks the regenerated text kept everything the
// hand-written one said.
//
// The regeneration is forced with onModelChange(), which is what any canvas
// edit (a drag, a tidy, a field change) runs; the save button posts the text
// editor's content as-is.
func TestE2EVisualSaveRoundTripLosesNothing(t *testing.T) {
	ts := startTestServer(t, roundTripConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, roundTripConfig)

	if _, err := page.Evaluate(`() => onModelChange()`); err != nil {
		t.Fatalf("onModelChange: %v", err)
	}
	// Wait until the text actually changed — regeneration renames variables
	// to node IDs, so the hand-written "rss_2 =" disappears.
	var out string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out = editorContent(t, page)
		if !strings.Contains(out, "rss_2 = input(") {
			break
		}
		page.Locator("#view-btn-visual").Click()
		time.Sleep(100 * time.Millisecond)
	}
	if strings.Contains(out, "rss_2 = input(") {
		t.Fatalf("the text was never regenerated; the test proves nothing:\n%s", out)
	}

	for _, want := range []string{
		// the detached section header
		"── tv: a section header, two lines long,",
		"separated from the node below by a blank line",
		// node comments, each on its own node
		"The first feed, by reference.",
		"The second feed, with a literal url of its own.",
		// the function-call comment
		"A comment above a function call.",
		// each node's own config, not its neighbour's
		`url="https://example.com/literal"`,
		"url=FEED",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("regenerated config lost %q", want)
		}
	}
	if t.Failed() {
		t.Logf("regenerated config:\n%s", out)
	}

	// And each comment sits above its own node: the literal-url comment must
	// be followed by the literal-url node, not the FEED one.
	i := strings.Index(out, "with a literal url of its own")
	if i >= 0 {
		rest := out[i:]
		nl := strings.Index(rest, " = input(")
		if nl < 0 || !strings.Contains(rest[:strings.Index(rest[nl:], "\n")+nl], "literal") {
			t.Errorf("the literal-url comment is not above the literal-url node:\n%s", out)
		}
	}
}
