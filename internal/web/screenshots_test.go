//go:build screenshots

package web_test

// screenshots_test.go captures UI screenshots for the user guide.
// Run with: go test -tags screenshots ./internal/web/... -run TestScreenshots -v

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	playwright "github.com/playwright-community/playwright-go"

	_ "github.com/brunoga/pipeliner/plugins/processor/filter/content"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/movies"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/trakt"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/upgrade"
	_ "github.com/brunoga/pipeliner/plugins/processor/metainfo/quality"
	_ "github.com/brunoga/pipeliner/plugins/processor/metainfo/tmdb"
	_ "github.com/brunoga/pipeliner/plugins/processor/metainfo/torrent"
	_ "github.com/brunoga/pipeliner/plugins/sink/transmission"
	_ "github.com/brunoga/pipeliner/plugins/source/trakt_list"
)

// screenshotOutDir returns docs/images/ relative to this source file.
func screenshotOutDir(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "docs", "images")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatalf("mkdir docs/images: %v", err)
	}
	return root
}

func screenshotPage(t *testing.T, page playwright.Page, dir, name string) {
	t.Helper()
	if _, err := page.Screenshot(playwright.PageScreenshotOptions{
		Path: playwright.String(filepath.Join(dir, name)),
	}); err != nil {
		t.Errorf("screenshot %s: %v", name, err)
	}
}

func screenshotLocator(t *testing.T, loc playwright.Locator, dir, name string) {
	t.Helper()
	if _, err := loc.Screenshot(playwright.LocatorScreenshotOptions{
		Path: playwright.String(filepath.Join(dir, name)),
	}); err != nil {
		t.Errorf("screenshot %s: %v", name, err)
	}
}

func waitLocatorVisible(t *testing.T, loc playwright.Locator) {
	t.Helper()
	if err := loc.WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(8000),
	}); err != nil {
		t.Fatalf("waitVisible: %v", err)
	}
}

func pause(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }

// selectNode clicks a node by its data-id and waits briefly for the panel.
func selectNode(t *testing.T, page playwright.Page, nodeID string) {
	t.Helper()
	loc := page.Locator(`.ve-node[data-id="` + nodeID + `"]`)
	if err := loc.Click(); err != nil {
		t.Fatalf("click node %q: %v", nodeID, err)
	}
	pause(300)
}

// ── configs ──────────────────────────────────────────────────────────────────

// normalConfig: rss → metainfo_quality → upgrade → transmission
// upgrade.Requires(video_quality) satisfied by metainfo_quality.Produces → no warnings.
const normalConfig = `
src  = input("rss", url="https://feeds.example.com/tv.rss")
q    = process("metainfo_quality", upstream=src)
up   = process("upgrade", upstream=q, target="1080p bluray")
sink = output("transmission", upstream=up, host="localhost")
pipeline("demo", schedule="1h")
`

// softWarnConfig: rss → metainfo_torrent → content → transmission
// content.Requires(torrent_files) satisfied only by metainfo_torrent.MayProduce → soft ~ warning.
const softWarnConfig = `
src   = input("rss", url="https://feeds.example.com/tv.rss")
meta  = process("metainfo_torrent", upstream=src)
ct    = process("content", upstream=meta, reject=["*.rar"])
sink  = output("transmission", upstream=ct, host="localhost")
pipeline("torrent-check", schedule="1h")
`

// hardWarnConfig: rss → upgrade (NO quality node).
// upgrade.Requires(video_quality) not satisfied → hard ⚠ error on node.
// Loaded into visual editor via text editor re-parse only (not saved).
const hardWarnConfig = `
src  = input("rss", url="https://feeds.example.com/tv.rss")
up   = process("upgrade", upstream=src, target="1080p bluray")
sink = output("transmission", upstream=up, host="localhost")
pipeline("demo")
`

// TestScreenshots generates UI screenshots for the user guide.
func TestScreenshots(t *testing.T) {
	dir := screenshotOutDir(t)
	browser, stop := pwSetup(t)
	defer stop()

	newPage := func(t *testing.T) playwright.Page {
		t.Helper()
		page, err := browser.NewPage()
		if err != nil {
			t.Fatalf("new page: %v", err)
		}
		if err := page.SetViewportSize(1400, 900); err != nil {
			t.Fatalf("set viewport: %v", err)
		}
		return page
	}

	// ── 1. Dashboard ────────────────────────────────────────────────────────────
	t.Run("dashboard", func(t *testing.T) {
		ts := startTestServer(t, normalConfig)
		page := newPage(t)
		defer page.Close() //nolint:errcheck
		login(t, page, ts.url)
		waitLocatorVisible(t, page.Locator(".task-name"))
		pause(500)
		screenshotPage(t, page, dir, "ui-dashboard.png")
	})

	// ── 2. Visual editor canvas ─────────────────────────────────────────────────
	t.Run("visual_editor", func(t *testing.T) {
		ts := startTestServer(t, normalConfig)
		page := newPage(t)
		defer page.Close() //nolint:errcheck
		login(t, page, ts.url)
		openConfigTab(t, page)
		switchToVisual(t, page, normalConfig)
		pause(400)
		screenshotLocator(t, page.Locator("#view-visual"), dir, "ui-visual-editor.png")
	})

	// ── 3. Param panel: Produces + May produce (metainfo_quality) ───────────────
	t.Run("param_panel_may_produce", func(t *testing.T) {
		ts := startTestServer(t, normalConfig)
		page := newPage(t)
		defer page.Close() //nolint:errcheck
		login(t, page, ts.url)
		openConfigTab(t, page)
		switchToVisual(t, page, normalConfig)
		// node IDs are pluginname_N; metainfo_quality is the second node (counter 1).
		selectNode(t, page, "metainfo_quality_1")
		waitLocatorVisible(t, page.Locator(".ve-field-hint-block").First())
		screenshotLocator(t, page.Locator("#view-visual"), dir, "ui-param-panel-may-produce.png")
	})

	// ── 4. Hard field warning: upgrade node without metainfo_quality ─────────────
	t.Run("hard_field_warning", func(t *testing.T) {
		// Start with valid config so the server loads, then load broken config
		// into the visual editor via text editor (re-parse, no save needed).
		ts := startTestServer(t, normalConfig)
		page := newPage(t)
		defer page.Close() //nolint:errcheck
		login(t, page, ts.url)
		openConfigTab(t, page)
		switchToVisual(t, page, hardWarnConfig)
		// upgrade node should show ⚠ amber warning.
		waitLocatorVisible(t, page.Locator(".ve-node-warn"))
		pause(200)
		screenshotLocator(t, page.Locator("#view-visual"), dir, "ui-field-warning-error.png")
	})

	// ── 5. Soft warning: content after metainfo_torrent ─────────────────────────
	t.Run("soft_field_warning", func(t *testing.T) {
		ts := startTestServer(t, softWarnConfig)
		page := newPage(t)
		defer page.Close() //nolint:errcheck
		login(t, page, ts.url)
		openConfigTab(t, page)
		switchToVisual(t, page, softWarnConfig)
		// content node shows ~ soft warning.
		waitLocatorVisible(t, page.Locator(".ve-node-soft-warn"))
		// content is the third node (counter 2).
		selectNode(t, page, "content_2")
		waitLocatorVisible(t, page.Locator(".ve-conn-soft-warn"))
		screenshotLocator(t, page.Locator("#view-visual"), dir, "ui-field-warning-soft.png")
	})

	// ── 6. Config editor with server-side validation warnings ───────────────────
	t.Run("validation_warnings", func(t *testing.T) {
		ts := startTestServer(t, softWarnConfig)
		page := newPage(t)
		defer page.Close() //nolint:errcheck
		login(t, page, ts.url)
		openConfigTab(t, page)
		if err := page.Locator("#config-editor").Fill(softWarnConfig); err != nil {
			t.Fatalf("fill: %v", err)
		}
		// Click the Validate button.
		if err := page.Locator("button.btn-config:not(.primary)").Click(); err != nil {
			t.Fatalf("validate click: %v", err)
		}
		waitLocatorVisible(t, page.Locator("#config-warnings"))
		pause(300)
		// #tab-config > section holds the toolbar, error/warning boxes, and editor.
		screenshotLocator(t, page.Locator("#tab-config > section"), dir, "ui-validation-warnings.png")
	})

	// ── 7. List-function palette chip with "list" badge ──────────────────────────
	t.Run("list_function_chip", func(t *testing.T) {
		ts := startTestServer(t, traktListFunctionConfig)
		page := newPage(t)
		defer page.Close() //nolint:errcheck
		login(t, page, ts.url)
		openConfigTab(t, page)
		switchToVisual(t, page, traktListFunctionConfig)
		// Wait for the function chip with the list badge to appear.
		waitLocatorVisible(t, page.Locator(`.ve-chip-fn.ve-chip-list`))
		pause(300)
		// Screenshot the palette so the list badge is clearly visible.
		screenshotLocator(t, page.Locator("#ve-palette-body"), dir, "ui-list-function-chip.png")
	})

	// ── 8. Movies node with mini-pipeline list source connected ──────────────────
	t.Run("list_function_connected", func(t *testing.T) {
		ts := startTestServer(t, traktListFunctionConfig)
		page := newPage(t)
		defer page.Close() //nolint:errcheck
		login(t, page, ts.url)
		openConfigTab(t, page)
		switchToVisual(t, page, traktListFunctionConfig)
		// Wait for the list sub-node (mini-pipeline) to appear below movies.
		waitLocatorVisible(t, page.Locator(`.ve-node.ve-node-list`))
		// Click the movies node to open its param panel.
		if err := page.Locator(`.ve-node-name:has-text("movies")`).First().Click(); err != nil {
			t.Fatalf("click movies node: %v", err)
		}
		waitLocatorVisible(t, page.Locator(`#ve-param-body .ve-node-list-badge`))
		pause(300)
		screenshotLocator(t, page.Locator("#view-visual"), dir, "ui-list-function-connected.png")
	})

}
