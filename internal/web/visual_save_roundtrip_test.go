package web_test

import (
	"strings"
	"testing"
	"time"

	playwright "github.com/mxschmitt/playwright-go"
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

	out := regenerate(t, page)

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
		// every hand-chosen variable name, not the loader's IDs
		"rss_1 = input(",
		"rss_2 = input(",
		"seen_9 = process(",
		"out = tail_fn(",
		"shown = output(",
		"also = output(",
		"upstream=seen_9",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("regenerated config lost %q", want)
		}
	}
	// The loader numbered the first feed rss_0; that ID must not leak out as
	// a variable name.
	if strings.Contains(out, "rss_0 =") {
		t.Errorf("a node was written under its loader ID instead of its variable")
	}

	// And each comment sits above its own node.
	if i := strings.Index(out, "with a literal url of its own"); i >= 0 {
		rest := out[i:]
		if j := strings.Index(rest, " = input("); j < 0 || !strings.HasPrefix(lastLine(rest[:j]), "rss_2") {
			t.Errorf("the literal-url comment is not above rss_2:\n%s", out)
		}
	}

	// A second save of the first save's output must change nothing. Anything
	// that drifts per save — a name, a comment, a separator — fails here even
	// if each save looks fine on its own.
	//
	// Re-parse the saved text into the model, then blank the editor so the
	// second rewrite can only come from the model — otherwise "unchanged"
	// would also be what no rewrite at all looks like.
	if _, err := page.Evaluate(`async () => { await textToVisualSync(); }`); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	if _, err := page.Evaluate(`() => { document.getElementById('config-editor').value = ''; }`); err != nil {
		t.Fatalf("clear editor: %v", err)
	}
	again := regenerate(t, page)
	if again != out {
		t.Errorf("a second save changed the file:\n--- first\n%s\n--- second\n%s", out, again)
	}

	if t.Failed() {
		t.Logf("regenerated config:\n%s", out)
	}
}

// regenerate forces the visual model to rewrite the text — what any canvas
// edit (a drag, a tidy, a field change) does — and returns the text Save &
// Reload would send. The hand-written fixture has no pipeliner:pos lines, so
// their appearance is the sign the rewrite happened.
func regenerate(t *testing.T, page playwright.Page) string {
	t.Helper()
	page.Locator("#view-btn-visual").Click()
	if _, err := page.Evaluate(`() => onModelChange()`); err != nil {
		t.Fatalf("onModelChange: %v", err)
	}
	var out string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out = editorContent(t, page)
		if strings.Contains(out, "# pipeliner:pos") {
			return out
		}
		page.Locator("#view-btn-visual").Click()
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the text was never regenerated; the test proves nothing:\n%s", out)
	return ""
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}
