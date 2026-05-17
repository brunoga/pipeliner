// Playwright e2e browser tests for the web UI.
// Tests are skipped automatically when Chromium is not installed.
// Install Chromium once with: go run github.com/playwright-community/playwright-go/cmd/playwright install chromium
package web_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	playwright "github.com/playwright-community/playwright-go"

	"github.com/brunoga/pipeliner/internal/config"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/web"

	// Register plugins needed by test configs.
	_ "github.com/brunoga/pipeliner/plugins/processor/discover"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/content"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/movies"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/seen"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/trakt"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/upgrade"
	_ "github.com/brunoga/pipeliner/plugins/processor/metainfo/quality"
	_ "github.com/brunoga/pipeliner/plugins/processor/metainfo/torrent"
	_ "github.com/brunoga/pipeliner/plugins/processor/modify/pathfmt"
	_ "github.com/brunoga/pipeliner/plugins/sink/print"
	_ "github.com/brunoga/pipeliner/plugins/sink/transmission"
	_ "github.com/brunoga/pipeliner/plugins/source/rss"
	_ "github.com/brunoga/pipeliner/plugins/source/rss_search"
	_ "github.com/brunoga/pipeliner/plugins/source/trakt_list"
)

// ── test server setup ────────────────────────────────────────────────────────

type testServer struct {
	url  string
	done chan struct{}
}

func startTestServer(t *testing.T, starConfig string) *testServer {
	t.Helper()

	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cfg, err := config.ParseBytes([]byte(starConfig))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	tasks, err := config.BuildTasks(cfg, db, nil)
	if err != nil {
		t.Fatalf("build tasks: %v", err)
	}

	infos := make([]web.TaskInfo, len(tasks))
	for i, tk := range tasks {
		infos[i] = web.TaskInfo{Name: tk.Name()}
	}

	hist := web.NewHistory()
	bcast := web.NewBroadcaster()
	srv := web.New(infos, &noopDaemon{}, hist, bcast, "test", "admin", "password")
	srv.SetStore(db)
	srv.SetConfigValidator(func(data []byte) ([]string, []string) {
		c, err := config.ParseBytes(data)
		if err != nil {
			return []string{err.Error()}, nil
		}
		errs, warns := config.Validate(c)
		toStrings := func(es []error) []string {
			s := make([]string, len(es))
			for i, e := range es {
				s[i] = e.Error()
			}
			return s
		}
		return toStrings(errs), toStrings(warns)
	})

	// Pick a free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() {
		defer close(done)
		_ = srv.Start(ctx, addr, nil)
	}()

	// Wait for the server to be ready.
	url := "http://" + addr
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "/login") //nolint:noctx
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	return &testServer{url: url, done: done}
}

type noopDaemon struct{}

func (d *noopDaemon) NextRun(_ string) time.Time { return time.Time{} }
func (d *noopDaemon) Trigger(_ string)           {}

// ── playwright helpers ───────────────────────────────────────────────────────

func pwSetup(t *testing.T) (playwright.Browser, func()) {
	t.Helper()
	pw, err := playwright.Run()
	if err != nil {
		t.Skipf("playwright not available: %v", err)
	}
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		pw.Stop() //nolint:errcheck
		t.Skipf("chromium not available: %v", err)
	}
	return browser, func() {
		browser.Close() //nolint:errcheck
		pw.Stop()       //nolint:errcheck
	}
}

func login(t *testing.T, page playwright.Page, baseURL string) {
	t.Helper()
	if _, err := page.Goto(baseURL + "/login"); err != nil {
		t.Fatalf("goto login: %v", err)
	}
	if err := page.Locator("#username").Fill("admin"); err != nil {
		t.Fatalf("fill username: %v", err)
	}
	if err := page.Locator("#password").Fill("password"); err != nil {
		t.Fatalf("fill password: %v", err)
	}
	if err := page.Locator(`button[type="submit"]`).Click(); err != nil {
		t.Fatalf("submit login: %v", err)
	}
	if err := page.WaitForURL(baseURL + "/"); err != nil {
		t.Fatalf("wait for redirect: %v", err)
	}
}

func openConfigTab(t *testing.T, page playwright.Page) {
	t.Helper()
	if err := page.Locator("#tab-btn-config").Click(); err != nil {
		t.Fatalf("click config tab: %v", err)
	}
	// Visual is now the default view; wait for the config tab container.
	if err := page.Locator("#tab-config").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("wait for config tab: %v", err)
	}
}

// ── tests ────────────────────────────────────────────────────────────────────

const minimalConfig = `
src = input("rss", url="https://example.com/rss")
flt = process("seen", upstream=src)
pipeline("tv")
`

func TestE2ELoginAndDashboard(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, err := browser.NewPage()
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)

	// Dashboard should show the task card (rendered after /api/status poll).
	if err := page.Locator(".task-name").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("wait for .task-name: %v", err)
	}
	taskName, err := page.Locator(".task-name").TextContent()
	if err != nil {
		t.Fatalf("task-name: %v", err)
	}
	if taskName != "tv" {
		t.Errorf("task name: got %q, want tv", taskName)
	}
}

func TestE2EConfigTabLoadsStarlark(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)

	// The text editor element must exist in the DOM (it lives in the hidden
	// text view; visual is the default view now).
	if err := page.Locator("#config-editor").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateAttached,
	}); err != nil {
		t.Errorf("config editor not found: %v", err)
	}
}

func TestE2EVisualToggleShowsPalette(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)

	if err := page.Locator("#view-btn-visual").Click(); err != nil {
		t.Fatalf("click visual toggle: %v", err)
	}

	// Plugin palette should be visible.
	if err := page.Locator("#ve-palette-body").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("plugin palette not visible after toggle: %v", err)
	}

	// Text view should be hidden.
	textVisible, err := page.Locator("#view-text").IsVisible()
	if err != nil {
		t.Fatalf("IsVisible: %v", err)
	}
	if textVisible {
		t.Error("text view should be hidden when visual mode is active")
	}
}

// addFirstPipelineThenChip switches to visual, creates a pipeline via the
// toolbar "+ Add pipeline" button, then clicks the first enabled chip.
func addFirstPipelineThenChip(t *testing.T, page playwright.Page) {
	t.Helper()
	if err := page.Locator("#view-btn-visual").Click(); err != nil {
		t.Fatalf("click visual toggle: %v", err)
	}
	// Wait for the toolbar button.
	if err := page.Locator("#ve-add-pipeline").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("add-pipeline toolbar button not visible: %v", err)
	}
	if err := page.Locator("#ve-add-pipeline").Click(); err != nil {
		t.Fatalf("click add-pipeline: %v", err)
	}
	// Palette chips should now be enabled.
	if err := page.Locator("#ve-palette-body .ve-chip:not([disabled])").First().WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("palette chips still disabled after adding pipeline: %v", err)
	}
	if err := page.Locator("#ve-palette-body .ve-chip:not([disabled])").First().Click(); err != nil {
		t.Fatalf("click palette chip: %v", err)
	}
}

func TestE2EVisualAddPluginFromPalette(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)

	addFirstPipelineThenChip(t, page)

	// A node card should appear in the canvas.
	if err := page.Locator(".ve-node").First().WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("no node card appeared in canvas: %v", err)
	}
}

func TestE2EVisualToTextSync(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)

	addFirstPipelineThenChip(t, page)
	page.Locator(".ve-node").First().WaitFor() //nolint:errcheck

	// Switch back to text view and verify the editor has content.
	if err := page.Locator("#view-btn-text").Click(); err != nil {
		t.Fatalf("click text toggle: %v", err)
	}
	editorContent, err := page.Locator("#config-editor").InputValue()
	if err != nil {
		t.Fatalf("get editor content: %v", err)
	}
	if editorContent == "" {
		t.Error("text editor is empty — visual changes should auto-sync to text")
	}
}

func TestE2ETextToVisualSync(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)

	// Switch to text view, fill, then switch to visual to trigger parse.
	page.Locator("#view-btn-text").Click() //nolint:errcheck
	starlark := fmt.Sprintf("src = input(\"rss\", url=%q)\npipeline(\"my-pipeline\")", "https://example.com")
	if err := page.Locator("#config-editor").Fill(starlark); err != nil {
		t.Fatalf("fill editor: %v", err)
	}
	page.Locator("#view-btn-visual").Click() //nolint:errcheck

	// An rss node card should appear in the canvas.
	if err := page.Locator(`.ve-node-name:has-text("rss")`).WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("rss node not found after Text→Visual sync: %v", err)
	}

	// The pipeline name label in the canvas should reflect the name from the config.
	if err := page.Locator(`.ve-pl-name:has-text("my-pipeline")`).WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("pipeline name label not found after Text→Visual sync: %v", err)
	}
}

func TestE2EValidateConfig(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)

	// Put valid DAG Starlark in the editor.
	page.Locator("#view-btn-text").Click()                                                                          //nolint:errcheck
	page.Locator("#config-editor").Fill("src = input(\"rss\", url=\"https://example.com\")\npipeline(\"p\")") //nolint:errcheck

	// Click Validate.
	if err := page.Locator(`button:has-text("Validate")`).Click(); err != nil {
		t.Fatalf("click validate: %v", err)
	}

	// Status should show success.
	if err := page.Locator(".config-status.ok").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("expected ok status after validating correct config: %v", err)
	}
}

// ── helpers for new tests ─────────────────────────────────────────────────────

// switchToVisual loads a DAG config into the text editor and switches to visual mode.
func switchToVisual(t *testing.T, page playwright.Page, starlark string) {
	t.Helper()
	// Switch to text view first so the editor is visible and fillable.
	page.Locator("#view-btn-text").Click() //nolint:errcheck
	if err := page.Locator("#config-editor").Fill(starlark); err != nil {
		t.Fatalf("fill editor: %v", err)
	}
	page.Locator("#view-btn-visual").Click() //nolint:errcheck
	if err := page.Locator(".ve-node").First().WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("wait for node card: %v", err)
	}
}

// editorContent returns the current text in the config editor textarea.
func editorContent(t *testing.T, page playwright.Page) string {
	t.Helper()
	page.Locator("#view-btn-text").Click() //nolint:errcheck
	v, err := page.Locator("#config-editor").InputValue()
	if err != nil {
		t.Fatalf("get editor content: %v", err)
	}
	return v
}

// ── comment editor tests ──────────────────────────────────────────────────────

const dagConfig = `src = input("rss", url="https://example.com/rss")
flt = process("seen", upstream=src)
pipeline("tv")`

func TestE2ENodeCommentButtonExists(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, dagConfig)

	// Hover over a node card to reveal the comment button.
	if err := page.Locator(".ve-node").First().Hover(); err != nil {
		t.Fatalf("hover over node: %v", err)
	}
	if err := page.Locator(".ve-node-comment-btn").First().WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("comment button not visible on node hover: %v", err)
	}
}

func TestE2ENodeCommentOpenModal(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, dagConfig)

	// Hover then click the comment button.
	page.Locator(".ve-node").First().Hover()             //nolint:errcheck
	page.Locator(".ve-node-comment-btn").First().Click() //nolint:errcheck

	// Modal should appear.
	if err := page.Locator(".ve-text-popup").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("comment modal did not open: %v", err)
	}
	// Modal textarea should be focused.
	if err := page.Locator(".ve-text-popup-ta").WaitFor(); err != nil {
		t.Errorf("modal textarea not found: %v", err)
	}
}

func TestE2ENodeCommentSavesToTextEditor(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, dagConfig)

	// Open comment modal on the first node.
	page.Locator(".ve-node").First().Hover()              //nolint:errcheck
	page.Locator(".ve-node-comment-btn").First().Click()  //nolint:errcheck
	page.Locator(".ve-text-popup-ta").WaitFor()   //nolint:errcheck

	// Type a comment and save.
	if err := page.Locator(".ve-text-popup-ta").Fill("My test comment"); err != nil {
		t.Fatalf("fill comment: %v", err)
	}
	if err := page.Locator(".ve-text-popup-save").Click(); err != nil {
		t.Fatalf("click save: %v", err)
	}

	// Switch back to text view and verify comment is present.
	content := editorContent(t, page)
	if !contains(content, "# My test comment") {
		t.Errorf("comment not found in text editor output:\n%s", content)
	}
}

func TestE2EPipelineCommentButtonExists(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, dagConfig)

	// Pipeline label should have a comment button.
	if err := page.Locator(".ve-pl-comment-btn").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateAttached,
	}); err != nil {
		t.Errorf("pipeline comment button not found: %v", err)
	}
}

func TestE2EPipelineCommentSavesToTextEditor(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, dagConfig)

	// Open the pipeline comment modal.
	page.Locator(".ve-pl-comment-btn").Click()   //nolint:errcheck
	page.Locator(".ve-text-popup-ta").WaitFor()  //nolint:errcheck

	if err := page.Locator(".ve-text-popup-ta").Fill("Pipeline description"); err != nil {
		t.Fatalf("fill pipeline comment: %v", err)
	}
	page.Locator(".ve-text-popup-save").Click() //nolint:errcheck

	content := editorContent(t, page)
	if !contains(content, "# Pipeline description") {
		t.Errorf("pipeline comment not in text editor:\n%s", content)
	}
}

func TestE2ECommentModalCancelDoesNotSave(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, dagConfig)

	page.Locator(".ve-node").First().Hover()                              //nolint:errcheck
	page.Locator(".ve-node-comment-btn").First().Click()                  //nolint:errcheck
	page.Locator(".ve-text-popup-ta").WaitFor()                   //nolint:errcheck
	page.Locator(".ve-text-popup-ta").Fill("Should not appear")   //nolint:errcheck
	page.Locator(".ve-text-popup-cancel").Click()                 //nolint:errcheck

	// Modal should close.
	if visible, _ := page.Locator(".ve-text-popup").IsVisible(); visible {
		t.Error("modal still visible after cancel")
	}
	// Text editor should not contain the comment.
	content := editorContent(t, page)
	if contains(content, "Should not appear") {
		t.Errorf("cancelled comment appeared in text editor")
	}
}

// ── layout persistence tests ──────────────────────────────────────────────────

func TestE2ELayoutCommentAppearsInTextEditor(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, dagConfig)

	// After visual sync, switching back to text should include per-node pos comments.
	content := editorContent(t, page)
	if !contains(content, "# pipeliner:pos") {
		t.Errorf("pipeliner:pos comment missing from text editor output:\n%s", content)
	}
}

func TestE2ELayoutRoundTripPreservesNodes(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)

	// Load a config that already has per-node pos comments (new format).
	configWithLayout := `# pipeliner:pos 50 76
src_0 = input("rss", url="https://example.com/rss")

# pipeliner:pos 310 76
flt_1 = process("seen", upstream=src_0)

pipeline("tv")`

	page.Locator("#view-btn-text").Click() //nolint:errcheck
	if err := page.Locator("#config-editor").Fill(configWithLayout); err != nil {
		t.Fatalf("fill editor: %v", err)
	}
	page.Locator("#view-btn-visual").Click() //nolint:errcheck

	// Both nodes should appear.
	if err := page.Locator(`.ve-node-name:has-text("rss")`).WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("rss node not visible: %v", err)
	}
	if err := page.Locator(`.ve-node-name:has-text("seen")`).WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("seen node not visible: %v", err)
	}
}

// ── pipeline regions and add pipeline ────────────────────────────────────────

func TestE2EPipelineRegionVisible(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, dagConfig)

	// At least one pipeline region background should be rendered.
	if err := page.Locator(".ve-pipeline-region").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateAttached,
	}); err != nil {
		t.Errorf("pipeline region not rendered: %v", err)
	}
}

func TestE2EAddPipelineCreatesNewRegion(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, dagConfig)

	// Count existing regions.
	regionsBefore, err := page.Locator(".ve-pipeline-region").All()
	if err != nil {
		t.Fatalf("count regions: %v", err)
	}

	// Click the toolbar add-pipeline button.
	if err := page.Locator("#ve-add-pipeline").Click(); err != nil {
		t.Fatalf("click add pipeline: %v", err)
	}

	// A new region should appear.
	if err := page.Locator(fmt.Sprintf(".ve-pipeline-region:nth-child(%d)", len(regionsBefore)+1)).WaitFor(
		playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateAttached},
	); err != nil {
		t.Logf("note: nth-child selector may differ; checking count instead")
		regionsAfter, _ := page.Locator(".ve-pipeline-region").All()
		if len(regionsAfter) <= len(regionsBefore) {
			t.Errorf("region count: got %d, want >%d", len(regionsAfter), len(regionsBefore))
		}
	}
}

func TestE2EMultiplePipelinesShowMultipleRegions(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)

	// Two-pipeline config.
	twoPipelines := `src_a = input("rss", url="https://a.example.com/rss")
pipeline("pipe-a")

src_b = input("rss", url="https://b.example.com/rss")
pipeline("pipe-b")`

	switchToVisual(t, page, twoPipelines)

	regions, err := page.Locator(".ve-pipeline-region").All()
	if err != nil {
		t.Fatalf("query regions: %v", err)
	}
	if len(regions) < 2 {
		t.Errorf("expected at least 2 pipeline regions, got %d", len(regions))
	}
}

func TestE2EPipelineBoundariesDistinct(t *testing.T) {
	// Separator bars have been replaced by pipeline region containers. Verify
	// that two pipelines each get their own region box (no separator needed).
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)

	twoPipelines := `src_a = input("rss", url="https://a.example.com/rss")
pipeline("pipe-a")

src_b = input("rss", url="https://b.example.com/rss")
pipeline("pipe-b")`

	switchToVisual(t, page, twoPipelines)

	// Each pipeline should have its own region container.
	regions, err := page.Locator(".ve-pipeline-region").All()
	if err != nil {
		t.Fatalf("query regions: %v", err)
	}
	if len(regions) < 2 {
		t.Errorf("expected ≥2 pipeline regions for two pipelines, got %d", len(regions))
	}
}

// ── search port ───────────────────────────────────────────────────────────────

func TestE2EViaPortVisibleOnDiscoverNode(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)

	// A config with a discover node.
	discoverConfig := `titles = input("rss", url="https://example.com/rss")
disc = process("discover", upstream=titles, interval="24h",
  search=[{"name": "rss_search", "url_template": "https://nyaa.si/?page=rss&q={QueryEscaped}"}])
pipeline("tv")`

	switchToVisual(t, page, discoverConfig)

	// The discover node should have a search port.
	if err := page.Locator(".ve-node-search-port").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateAttached,
	}); err != nil {
		t.Errorf("search port not rendered on discover node: %v", err)
	}
}

func TestE2EViaNodeAppearsAfterParseWithVia(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)

	// Config with a discover node that has a search backend.
	discoverConfig := `titles = input("rss", url="https://example.com/rss")
disc = process("discover", upstream=titles, interval="24h",
  search=[{"name": "rss_search", "url_template": "https://nyaa.si/?page=rss&q={QueryEscaped}"}])
pipeline("tv")`

	switchToVisual(t, page, discoverConfig)

	// A node with the "rss_search" plugin should appear (search-connected).
	if err := page.Locator(`.ve-node-name:has-text("rss_search")`).WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("search-connected rss_search node not visible: %v", err)
	}
}

// ── search-plugin palette badge ───────────────────────────────────────────────

func TestE2ESearchPluginHasViaBadge(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)

	// Need a pipeline so the palette is enabled (shows full chips with badges).
	page.Locator("#view-btn-visual").Click()   //nolint:errcheck
	page.Locator("#ve-add-pipeline").WaitFor() //nolint:errcheck
	page.Locator("#ve-add-pipeline").Click()   //nolint:errcheck

	// At least one palette chip should carry a "search" badge.
	if err := page.Locator(".ve-chip-search-badge").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("no search badge found in palette — search plugins should show one: %v", err)
	}
}

// ── field-hint / warning tests ───────────────────────────────────────────────

// jsSelectNode calls the visual editor's selectNode(id) function directly.
// More reliable than DOM clicking for absolutely-positioned canvas nodes.
func jsSelectNode(t *testing.T, page playwright.Page, nodeID string) {
	t.Helper()
	if _, err := page.Evaluate("selectNode('" + nodeID + "')"); err != nil {
		t.Fatalf("jsSelectNode(%q): %v", nodeID, err)
	}
}

// waitVisible waits up to 5 s for a locator to become visible.
func waitVisible(t *testing.T, loc playwright.Locator) {
	t.Helper()
	if err := loc.WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(5000),
	}); err != nil {
		t.Fatalf("waitVisible: %v", err)
	}
}

// TestE2EParamPanelShowsMayProduce verifies that selecting a node with both
// Produces and MayProduce fields shows both sections in the param panel.
func TestE2EParamPanelShowsMayProduce(t *testing.T) {
	// rss → metainfo_quality (has Produces + MayProduce) → upgrade → transmission
	const cfg = `
src  = input("rss", url="https://feeds.example.com/tv.rss")
q    = process("metainfo_quality", upstream=src)
up   = process("upgrade", upstream=q, target="1080p bluray")
sink = output("transmission", upstream=up, host="localhost")
pipeline("demo", schedule="1h")
`
	ts := startTestServer(t, cfg)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, cfg)

	// metainfo_quality is the second node, ID = metainfo_quality_1.
	jsSelectNode(t, page, "metainfo_quality_1")

	// Param panel must show a plain Produces block.
	waitVisible(t, page.Locator(".ve-field-hint-block").First())

	// Param panel must also show the dimmed May produce block.
	waitVisible(t, page.Locator(".ve-field-hint-maybe"))

	// Sanity: the Produces block must mention video_quality.
	text, err := page.Locator(".ve-field-hint-block").First().TextContent()
	if err != nil {
		t.Fatalf("get produces text: %v", err)
	}
	if !contains(text, "video_quality") {
		t.Errorf("expected Produces block to mention video_quality, got: %q", text)
	}

	// Sanity: the May produce block must mention codec.
	mayText, err := page.Locator(".ve-field-hint-maybe").TextContent()
	if err != nil {
		t.Fatalf("get may-produce text: %v", err)
	}
	if !contains(mayText, "codec") {
		t.Errorf("expected May produce block to mention codec, got: %q", mayText)
	}
}

// TestE2EHardFieldWarningOnNode verifies that a node whose required field has
// no upstream producer shows an amber ⚠ warning inline on the canvas card.
func TestE2EHardFieldWarningOnNode(t *testing.T) {
	// upgrade requires video_quality, but rss doesn't produce it → hard warning.
	// This config is only loaded into the visual editor (not saved), so the server
	// can start with any valid config.
	const validCfg = `
src = input("rss", url="https://feeds.example.com/tv.rss")
sink = output("transmission", upstream=src, host="localhost")
pipeline("base")
`
	const warnCfg = `
src  = input("rss", url="https://feeds.example.com/tv.rss")
up   = process("upgrade", upstream=src, target="1080p bluray")
sink = output("transmission", upstream=up, host="localhost")
pipeline("demo")
`
	ts := startTestServer(t, validCfg)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)
	// Load the broken config into the visual editor without saving.
	switchToVisual(t, page, warnCfg)

	// upgrade node must show the amber ⚠ hard warning.
	waitVisible(t, page.Locator(".ve-node-warn"))

	text, err := page.Locator(".ve-node-warn").First().TextContent()
	if err != nil {
		t.Fatalf("get warn text: %v", err)
	}
	if !contains(text, "video_quality") {
		t.Errorf("expected hard warning to mention video_quality, got: %q", text)
	}
}

// TestE2ESoftFieldWarningOnNode verifies that a node whose required field is
// only conditionally produced upstream shows a muted ~ soft warning on the
// canvas card and a detailed soft warning in the param panel.
func TestE2ESoftFieldWarningOnNode(t *testing.T) {
	// content requires torrent_files; metainfo_torrent only MayProduces it → soft warning.
	const cfg = `
src   = input("rss", url="https://feeds.example.com/tv.rss")
meta  = process("metainfo_torrent", upstream=src)
ct    = process("content", upstream=meta, reject=["*.rar"])
sink  = output("transmission", upstream=ct, host="localhost")
pipeline("torrent-check", schedule="1h")
`
	ts := startTestServer(t, cfg)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, cfg)

	// content node (counter 2) must show the muted ~ soft warning on its card.
	waitVisible(t, page.Locator(".ve-node-soft-warn"))

	// Select the content node and verify the detailed soft warning in the param panel.
	jsSelectNode(t, page, "content_2")
	waitVisible(t, page.Locator(".ve-conn-soft-warn"))

	detailText, err := page.Locator(".ve-conn-soft-warn").TextContent()
	if err != nil {
		t.Fatalf("get soft warn detail: %v", err)
	}
	if !contains(detailText, "torrent_files") {
		t.Errorf("expected soft warning to mention torrent_files, got: %q", detailText)
	}
}

// ── utility ───────────────────────────────────────────────────────────────────

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || stringContains(s, sub))
}
func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ── function editor edge regression test ─────────────────────────────────────

// TestE2EFunctionEditorEdgesDrawnOnOpen reproduces the bug where edges inside
// the function body editor are not drawn until the user clicks somewhere.
// The test opens the function editor and immediately inspects the SVG to check
// whether ve-edge paths are present without any user interaction.
func TestE2EFunctionEditorEdgesDrawnOnOpen(t *testing.T) {
	// Two-node function body (rss → seen) so exactly one edge should be drawn.
	const cfg = `
# pipeliner:param url  type=string  Feed URL
def fetch_fn(url):
    src_0 = input("rss", url=url)
    flt_1 = process("seen", upstream=src_0)
    return flt_1

call_2 = fetch_fn(url="https://example.com/rss")
output("print", upstream=call_2)
pipeline("test")
`
	ts := startTestServer(t, cfg)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, cfg)

	// Click the ✏ edit button on the function chip in the palette.
	editBtn := page.Locator(".ve-chip-fn-edit")
	if err := editBtn.WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(5000),
	}); err != nil {
		t.Fatalf("edit button not visible: %v", err)
	}
	if err := editBtn.Click(); err != nil {
		t.Fatalf("click edit button: %v", err)
	}

	// Wait for the function editor banner to appear.
	if err := page.Locator("#ve-fn-bar").WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(5000),
	}); err != nil {
		t.Fatalf("function editor bar not visible: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// The edge between the two function body nodes must be present on open,
	// without requiring any user interaction.
	// Use a string result to avoid Go type assertion issues with JS numbers.
	edgeCountStr, err := page.Evaluate(`String(document.querySelectorAll('#ve-graph-svg path.ve-edge').length)`)
	if err != nil {
		t.Fatalf("evaluate edge count: %v", err)
	}
	t.Logf("edge count on open: %v", edgeCountStr)
	if edgeCountStr == "0" || edgeCountStr == nil {
		// Check if clicking makes it appear (the original bug).
		if err := page.Locator("#ve-canvas-body").Click(playwright.LocatorClickOptions{
			Position: &playwright.Position{X: 300, Y: 200},
		}); err == nil {
			time.Sleep(100 * time.Millisecond)
			afterStr, _ := page.Evaluate(`String(document.querySelectorAll('#ve-graph-svg path.ve-edge').length)`)
			t.Logf("edge count after click: %v", afterStr)
			if afterStr != "0" && afterStr != nil {
				t.Errorf("BUG: edge only appears after a click, not on open")
			} else {
				t.Errorf("edge never appears — check parseFunctionBodyNodes and server config")
			}
		}
	}
	// else: edge is visible on open — test passes
}

// ── list-function connection test ─────────────────────────────────────────────

// traktListFunctionConfig is a DAG config where:
//   - unwatched_movies is a user function wrapping trakt_list → trakt
//   - a movies node uses it via list=[unwatched_movies()]
const traktListFunctionConfig = `
# pipeliner:param
def unwatched_movies():
    src      = input("trakt_list", client_id="test-id", type="movies")
    filtered = process("trakt", upstream=src, client_id="test-id", type="movies", list="history", reject_matched=True, reject_unmatched=False)
    return filtered

main_src = input("rss", url="https://example.com/rss")
seen     = process("seen", upstream=main_src)
flt      = process("movies", upstream=seen, list=[unwatched_movies()], static=["Inception"])
output("print", upstream=flt)
pipeline("unwatched", schedule="1h")
`

// TestPlaywrightListFunctionBadgeAndConnection verifies that:
//  1. A user function whose body contains a trakt_list input gets a "list"
//     badge on its palette chip (is_list_plugin propagated from Go → JSON → UI).
//  2. The movies node renders the function's mini-pipeline as a connected list
//     sub-node (the trakt_list→trakt chain appears below movies on the canvas).
//  3. Clicking the movies node shows the list-source in the param panel.
func TestPlaywrightListFunctionBadgeAndConnection(t *testing.T) {
	ts := startTestServer(t, traktListFunctionConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, traktListFunctionConfig)

	// 1. The palette function chip must carry the "list" CSS class and badge.
	//    This confirms is_list_plugin was detected server-side and sent to the UI.
	if err := page.Locator(`.ve-chip-fn.ve-chip-list`).WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("function chip missing 'list' class — is_list_plugin not propagated from Go to UI: %v", err)
	}

	// 2. The movies node must have a list sub-node on the canvas showing the
	//    mini-pipeline chain (trakt_list→trakt). It carries the ve-node-list CSS
	//    class because isListNode=true.
	if err := page.Locator(`.ve-node.ve-node-list`).WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("no list sub-node visible on canvas — mini-pipeline not shown as connected list source: %v", err)
	}

	// 3. Click the movies node; the param panel must show the list badge for
	//    the connected mini-pipeline source.
	if err := page.Locator(`.ve-node-name:has-text("movies")`).First().Click(); err != nil {
		t.Fatalf("click movies node: %v", err)
	}
	// Use a scoped locator to avoid strict-mode violation (there are two
	// .ve-node-list-badge elements: one in the canvas sub-node, one in the panel).
	if err := page.Locator(`#ve-param-body .ve-node-list-badge`).WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("no list badge in movies param panel — function not shown as connected list source: %v", err)
	}
}
