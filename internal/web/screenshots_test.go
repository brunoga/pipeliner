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
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/premiere"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/trakt"
	_ "github.com/brunoga/pipeliner/plugins/processor/metainfo/file"
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

// normalConfig: rss → metainfo_file → premiere → transmission.
// premiere.Requires(...) satisfied by metainfo_file.Produces → no warnings.
const normalConfig = `
src  = input("rss", url="https://feeds.example.com/tv.rss")
q    = process("metainfo_file", upstream=src)
up   = process("premiere", upstream=q)
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

// hardWarnConfig: rss → premiere (NO metainfo_file node).
// premiere.Requires(...) not satisfied → hard ⚠ error on node.
// Loaded into visual editor via text editor re-parse only (not saved).
const hardWarnConfig = `
src  = input("rss", url="https://feeds.example.com/tv.rss")
up   = process("premiere", upstream=src)
sink = output("transmission", upstream=up, host="localhost")
pipeline("demo")
`

// routeScreenshotConfig: rss → seen → metainfo_file → route() with tv/movies ports.
// Shows the route() branching pattern in the visual editor.
const routeScreenshotConfig = `
src    = input("rss", url="https://feeds.example.com/all.rss")
seen   = process("seen", upstream=src)
q      = process("metainfo_file", upstream=seen)
routes = route(q,
    tv     = "series_episode_id != ''",
    movies = "series_episode_id == ''")
tv_out    = output("print", upstream=routes.tv)
movie_out = output("print", upstream=routes.movies)
pipeline("route-demo", schedule="1h")
`

// routePortContractsConfig: routes by media_type using real fields produced by
// metainfo_file. Each port's presence/equality check promotes the field on
// that branch, which the param panel displays as a "Promotes to certain"
// annotation (.ve-cond-narrow).
const routePortContractsConfig = `
src    = input("rss", url="https://feeds.example.com/all.rss")
seen   = process("seen", upstream=src)
meta   = process("metainfo_file", upstream=seen)
routes = route(meta,
    series = "media_type == 'series'",
    movies = "media_type == 'movie'")
series_out = output("print", upstream=routes.series)
movies_out = output("print", upstream=routes.movies)
pipeline("port-contracts-demo", schedule="1h")
`

// fnEditorConfig: a pipeline with a user function so we can open the fn editor.
const fnEditorConfig = `
def enrich(upstream):
    q = process("metainfo_file", upstream=upstream)
    return q

src    = input("rss", url="https://feeds.example.com/tv.rss")
seen   = process("seen", upstream=src)
result = enrich(upstream=seen)
sink   = output("print", upstream=result)
pipeline("fn-demo", schedule="1h")
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
		// Headless Chromium defaults to prefers-color-scheme: light, which
		// triggers our light-mode CSS override. Force dark so screenshots
		// always show the intended dark theme.
		if err := page.EmulateMedia(playwright.PageEmulateMediaOptions{
			ColorScheme: playwright.ColorSchemeDark,
		}); err != nil {
			t.Fatalf("emulate dark: %v", err)
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

	// ── 3. Param panel: Produces + May produce (metainfo_file) ───────────────
	t.Run("param_panel_may_produce", func(t *testing.T) {
		ts := startTestServer(t, normalConfig)
		page := newPage(t)
		defer page.Close() //nolint:errcheck
		login(t, page, ts.url)
		openConfigTab(t, page)
		switchToVisual(t, page, normalConfig)
		// node IDs are pluginname_N; metainfo_file is the second node (counter 1).
		selectNode(t, page, "metainfo_file_1")
		waitLocatorVisible(t, page.Locator(".ve-field-hint-block").First())
		screenshotLocator(t, page.Locator("#view-visual"), dir, "ui-param-panel-may-produce.png")
	})

	// ── 4. Hard field warning: premiere node without metainfo_file ─────────────
	t.Run("hard_field_warning", func(t *testing.T) {
		// Start with valid config so the server loads, then load broken config
		// into the visual editor via text editor (re-parse, no save needed).
		ts := startTestServer(t, normalConfig)
		page := newPage(t)
		defer page.Close() //nolint:errcheck
		login(t, page, ts.url)
		openConfigTab(t, page)
		switchToVisual(t, page, hardWarnConfig)
		// premiere node should show ⚠ amber warning.
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
		// Switch to text view before filling — visual editor is the default.
		page.Locator("#view-btn-text").Click() //nolint:errcheck
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

	// ── 9. route() branching in the visual editor ─────────────────────────────
	t.Run("route_visual", func(t *testing.T) {
		ts := startTestServer(t, normalConfig)
		page := newPage(t)
		defer page.Close() //nolint:errcheck
		login(t, page, ts.url)
		openConfigTab(t, page)
		switchToVisual(t, page, routeScreenshotConfig)
		// Wait for route node to render.
		waitLocatorVisible(t, page.Locator(`.ve-node-name:has-text("route")`).First())
		pause(400)
		screenshotLocator(t, page.Locator("#view-visual"), dir, "ui-route-visual.png")
	})

	// ── 10. Settings tab — Trakt authorization form ────────────────────────────
	t.Run("settings_tab", func(t *testing.T) {
		ts := startTestServer(t, normalConfig)
		page := newPage(t)
		defer page.Close() //nolint:errcheck
		login(t, page, ts.url)
		if err := page.Locator("#tab-btn-settings").Click(); err != nil {
			t.Fatalf("click settings tab: %v", err)
		}
		waitLocatorVisible(t, page.Locator("#tab-settings"))
		pause(300)
		screenshotLocator(t, page.Locator("#tab-settings"), dir, "ui-settings-tab.png")
	})

	// ── 11. Database tab — series/movie/seen trackers ─────────────────────────
	t.Run("database_tab", func(t *testing.T) {
		ts := startTestServer(t, normalConfig)
		page := newPage(t)
		defer page.Close() //nolint:errcheck
		login(t, page, ts.url)
		if err := page.Locator("#tab-btn-db").Click(); err != nil {
			t.Fatalf("click database tab: %v", err)
		}
		waitLocatorVisible(t, page.Locator("#tab-db"))
		pause(400)
		screenshotPage(t, page, dir, "ui-database-tab.png")
	})

	// ── 12. Text / config editor — fills the viewport height ─────────────────
	t.Run("text_editor", func(t *testing.T) {
		ts := startTestServer(t, normalConfig)
		page := newPage(t)
		defer page.Close() //nolint:errcheck
		login(t, page, ts.url)
		openConfigTab(t, page)
		// Switch to text view.
		if err := page.Locator("#view-btn-text").Click(); err != nil {
			t.Fatalf("click text view: %v", err)
		}
		waitLocatorVisible(t, page.Locator("#view-text"))
		if err := page.Locator("#config-editor").Fill(normalConfig); err != nil {
			t.Fatalf("fill editor: %v", err)
		}
		pause(400)
		screenshotLocator(t, page.Locator("#tab-config > section"), dir, "ui-text-editor.png")
	})

	// ── 13. route() with port() field contracts — param panel sub-rows ────────
	t.Run("route_port_contracts", func(t *testing.T) {
		ts := startTestServer(t, normalConfig)
		page := newPage(t)
		defer page.Close() //nolint:errcheck
		login(t, page, ts.url)
		openConfigTab(t, page)
		switchToVisual(t, page, routePortContractsConfig)
		// Wait for route node then click it to open the param panel.
		waitLocatorVisible(t, page.Locator(`.ve-node-name:has-text("route")`).First())
		if err := page.Locator(`.ve-node[data-id="route_3"]`).Click(); err != nil {
			// Fallback: click the first route node found.
			if err2 := page.Locator(`.ve-node-name:has-text("route")`).First().Click(); err2 != nil {
				t.Fatalf("click route node: %v / %v", err, err2)
			}
		}
		// Wait for the field-inference annotation to appear in the param panel
		// (".ve-cond-narrow" is the "Promotes to certain: …" sub-row).
		waitLocatorVisible(t, page.Locator(".ve-cond-narrow").First())
		pause(400)
		screenshotLocator(t, page.Locator("#view-visual"), dir, "ui-route-port-contracts.png")
	})

	// ── 14. Multi-select — nodes selected with Copy/Cut/Undo toolbar ──────────
	t.Run("multi_select", func(t *testing.T) {
		ts := startTestServer(t, normalConfig)
		page := newPage(t)
		defer page.Close() //nolint:errcheck
		login(t, page, ts.url)
		openConfigTab(t, page)
		switchToVisual(t, page, normalConfig)
		waitLocatorVisible(t, page.Locator(".ve-node").First())
		pause(400)

		// Click first node, then Cmd/Ctrl+click second node to multi-select.
		if err := page.Locator(`.ve-node[data-id="metainfo_file_1"]`).Click(); err != nil {
			t.Fatalf("click first node: %v", err)
		}
		pause(200)
		if err := page.Keyboard().Down("Meta"); err != nil {
			t.Fatalf("meta down: %v", err)
		}
		if err := page.Locator(`.ve-node[data-id="premiere_2"]`).Click(); err != nil {
			// Try Ctrl key on non-Mac.
			page.Keyboard().Up("Meta") //nolint:errcheck
			page.Keyboard().Down("Control") //nolint:errcheck
			page.Locator(`.ve-node[data-id="premiere_2"]`).Click() //nolint:errcheck
			page.Keyboard().Up("Control") //nolint:errcheck
		} else {
			page.Keyboard().Up("Meta") //nolint:errcheck
		}
		// Wait for Copy and Cut buttons to appear.
		waitLocatorVisible(t, page.Locator("#ve-copy-btn"))
		pause(300)
		screenshotLocator(t, page.Locator("#view-visual"), dir, "ui-multi-select.png")
	})

	// ── 15. Function editor — two-row toolbar with fn name and Params panel ───
	// Uses traktListFunctionConfig which reliably produces a function chip.
	t.Run("fn_editor", func(t *testing.T) {
		ts := startTestServer(t, traktListFunctionConfig)
		page := newPage(t)
		defer page.Close() //nolint:errcheck
		login(t, page, ts.url)
		openConfigTab(t, page)
		switchToVisual(t, page, traktListFunctionConfig)
		// Wait for the function chip to appear in the palette.
		waitLocatorVisible(t, page.Locator(`.ve-chip-fn`).First())
		pause(300)
		// Click the edit (pencil) button on the function chip.
		if err := page.Locator(`.ve-chip-fn-edit`).First().Click(); err != nil {
			t.Fatalf("click fn edit: %v", err)
		}
		// Wait for the fn-bar to become visible.
		waitLocatorVisible(t, page.Locator("#ve-fn-bar"))
		pause(200)
		// Open the params panel so the screenshot shows both toolbar rows.
		if err := page.Locator("#ve-fn-bar-params-btn").Click(); err != nil {
			t.Fatalf("click params btn: %v", err)
		}
		waitLocatorVisible(t, page.Locator("#ve-fn-params-panel"))
		pause(400)
		screenshotLocator(t, page.Locator("#view-visual"), dir, "ui-fn-editor.png")
	})

}
