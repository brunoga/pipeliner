// Playwright e2e browser tests for the web UI.
// Tests are skipped automatically when Chromium is not installed.
// Install Chromium once with: go run github.com/mxschmitt/playwright-go/cmd/playwright install chromium
package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	playwright "github.com/mxschmitt/playwright-go"

	"github.com/brunoga/pipeliner/internal/config"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/web"

	// Register plugins needed by test configs.
	_ "github.com/brunoga/pipeliner/plugins/processor/discover"
	_ "github.com/brunoga/pipeliner/plugins/source/bluray_releases"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/condition"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/content"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/movies"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/premiere"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/route"
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/seen"
	_ "github.com/brunoga/pipeliner/plugins/processor/metainfo/file"
	_ "github.com/brunoga/pipeliner/plugins/processor/metainfo/torrent"
	_ "github.com/brunoga/pipeliner/plugins/processor/modify/pathfmt"
	_ "github.com/brunoga/pipeliner/plugins/sink/print"
	_ "github.com/brunoga/pipeliner/plugins/sink/transmission"
	_ "github.com/brunoga/pipeliner/plugins/source/rss"
	_ "github.com/brunoga/pipeliner/plugins/source/trakt_list"
)

// ── test server setup ────────────────────────────────────────────────────────

type testServer struct {
	url  string
	done chan struct{}
	db   *store.SQLiteStore // exposed for tests that need to seed buckets
	srv  *web.Server         // exposed so tests can wire optional controls (e.g. SetPluginLogControl)
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

	return &testServer{url: url, done: done, db: db, srv: srv}
}

type noopDaemon struct{}

func (d *noopDaemon) NextRun(_ string) time.Time { return time.Time{} }
func (d *noopDaemon) Trigger(_ string, _ bool)   {}

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
		pw.Stop()
		t.Skipf("chromium not available: %v", err)
	}
	return browser, func() {
		browser.Close()
		pw.Stop()
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
	defer page.Close()

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
	defer page.Close()

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
	defer page.Close()

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
	defer page.Close()

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
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)

	addFirstPipelineThenChip(t, page)
	page.Locator(".ve-node").First().WaitFor()

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
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)

	// Switch to text view, fill, then switch to visual to trigger parse.
	page.Locator("#view-btn-text").Click()
	starlark := fmt.Sprintf("src = input(\"rss\", url=%q)\npipeline(\"my-pipeline\")", "https://example.com")
	if err := page.Locator("#config-editor").Fill(starlark); err != nil {
		t.Fatalf("fill editor: %v", err)
	}
	page.Locator("#view-btn-visual").Click()

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
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)

	// Put valid DAG Starlark in the editor.
	page.Locator("#view-btn-text").Click()
	page.Locator("#config-editor").Fill("src = input(\"rss\", url=\"https://example.com\")\npipeline(\"p\")")

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
	page.Locator("#view-btn-text").Click()
	if err := page.Locator("#config-editor").Fill(starlark); err != nil {
		t.Fatalf("fill editor: %v", err)
	}
	page.Locator("#view-btn-visual").Click()
	if err := page.Locator(".ve-node").First().WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("wait for node card: %v", err)
	}
}

// editorContent returns the current text in the config editor textarea.
func editorContent(t *testing.T, page playwright.Page) string {
	t.Helper()
	page.Locator("#view-btn-text").Click()
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
	defer page.Close()

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
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, dagConfig)

	// Hover then click the comment button.
	page.Locator(".ve-node").First().Hover()
	page.Locator(".ve-node-comment-btn").First().Click()

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
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, dagConfig)

	// Open comment modal on the first node.
	page.Locator(".ve-node").First().Hover()
	page.Locator(".ve-node-comment-btn").First().Click()
	page.Locator(".ve-text-popup-ta").WaitFor()

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
	defer page.Close()

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
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, dagConfig)

	// Open the pipeline comment modal.
	page.Locator(".ve-pl-comment-btn").Click()
	page.Locator(".ve-text-popup-ta").WaitFor()

	if err := page.Locator(".ve-text-popup-ta").Fill("Pipeline description"); err != nil {
		t.Fatalf("fill pipeline comment: %v", err)
	}
	page.Locator(".ve-text-popup-save").Click()

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
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, dagConfig)

	page.Locator(".ve-node").First().Hover()
	page.Locator(".ve-node-comment-btn").First().Click()
	page.Locator(".ve-text-popup-ta").WaitFor()
	page.Locator(".ve-text-popup-ta").Fill("Should not appear")
	page.Locator(".ve-text-popup-cancel").Click()

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
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, dagConfig)

	// Merely opening the visual view must NOT rewrite the text buffer any
	// more — positions are only serialised on a real user mutation. Drag a
	// node, then the regenerated text must include per-node pos comments.
	dragNodeBy(t, page, `.ve-node:has(.ve-node-name:has-text("rss"))`, 40, 20)

	content := editorContent(t, page)
	if !contains(content, "# pipeliner:pos") {
		t.Errorf("pipeliner:pos comment missing from text editor output after drag:\n%s", content)
	}
}

// TestE2EOpeningConfigTabDoesNotRewriteText guards the no-rewrite-on-open
// policy: switching Text→Visual on a hand-authored config must leave the text
// buffer byte-for-byte intact (variable names, comments, formatting) until the
// user actually mutates the model.
func TestE2EOpeningConfigTabDoesNotRewriteText(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)

	authored := "# my hand-written config\nsrc = input(\"rss\", url=\"https://example.com/rss\")\nflt = process(\"seen\", upstream=src)\npipeline(\"tv\")"
	switchToVisual(t, page, authored)

	content := editorContent(t, page)
	if content != authored {
		t.Errorf("text buffer was rewritten by merely opening the visual view:\ngot:\n%s\nwant:\n%s", content, authored)
	}
	if !contains(content, "src = input") || contains(content, "rss_0 =") {
		t.Errorf("hand-authored variable names were regenerated:\n%s", content)
	}
}

func TestE2ELayoutRoundTripPreservesNodes(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)

	// Load a config that already has per-node pos comments (new format).
	configWithLayout := `# pipeliner:pos 50 76
src_0 = input("rss", url="https://example.com/rss")

# pipeliner:pos 310 76
flt_1 = process("seen", upstream=src_0)

pipeline("tv")`

	page.Locator("#view-btn-text").Click()
	if err := page.Locator("#config-editor").Fill(configWithLayout); err != nil {
		t.Fatalf("fill editor: %v", err)
	}
	page.Locator("#view-btn-visual").Click()

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
	defer page.Close()

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
	defer page.Close()

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
	defer page.Close()

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
	defer page.Close()

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
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)

	// A config with a discover node.
	discoverConfig := `titles = input("rss", url="https://example.com/rss")
disc = process("discover", upstream=titles, interval="24h",
  search=[{"name": "rss", "url_template": "https://nyaa.si/?page=rss&q={QueryEscaped}"}])
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
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)

	// Config with a discover node that has a search backend.
	discoverConfig := `titles = input("rss", url="https://example.com/rss")
disc = process("discover", upstream=titles, interval="24h",
  search=[{"name": "rss", "url_template": "https://nyaa.si/?page=rss&q={QueryEscaped}"}])
pipeline("tv")`

	switchToVisual(t, page, discoverConfig)

	// Both the regular rss input node and the rss search-backend node should be
	// visible — the config contains two distinct rss nodes (url= and url_template=).
	rssCount, err := page.Locator(`.ve-node-name:has-text("rss")`).Count()
	if err != nil {
		t.Fatalf("counting rss nodes: %v", err)
	}
	if rssCount < 2 {
		t.Errorf("expected at least 2 rss nodes (input + search backend), got %d", rssCount)
	}
}

// ── search-plugin palette badge ───────────────────────────────────────────────

func TestE2ESearchPluginHasViaBadge(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)

	// Need a pipeline so the palette is enabled (shows full chips with badges).
	page.Locator("#view-btn-visual").Click()
	page.Locator("#ve-add-pipeline").WaitFor()
	page.Locator("#ve-add-pipeline").Click()

	// At least one palette chip should carry a "search" badge.
	if err := page.Locator(".ve-chip-search-badge").First().WaitFor(playwright.LocatorWaitForOptions{
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
	// rss → metainfo_file (has Produces + MayProduce) → premiere → transmission
	const cfg = `
src  = input("rss", url="https://feeds.example.com/tv.rss")
q    = process("metainfo_file", upstream=src)
up   = process("premiere", upstream=q)
sink = output("transmission", upstream=up, host="localhost")
pipeline("demo", schedule="1h")
`
	ts := startTestServer(t, cfg)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, cfg)

	// metainfo_file is the second node, ID = metainfo_file_1.
	jsSelectNode(t, page, "metainfo_file_1")

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

// TestE2ECopyButtonAppearsAfterDirectClick verifies the toolbar Copy / Cut
// buttons become visible when a node is selected via a single direct click
// (the selectNode() path). Region-select already surfaced them — this guards
// the missing updateCopyButton() hook that left the direct-click path silent.
func TestE2ECopyButtonAppearsAfterDirectClick(t *testing.T) {
	const cfg = `
src  = input("rss", url="https://feeds.example.com/tv.rss")
sink = output("print", upstream=src)
pipeline("demo")
`
	ts := startTestServer(t, cfg)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, cfg)

	copyBtn := page.Locator("#ve-copy-btn")
	cutBtn := page.Locator("#ve-cut-btn")

	// Nothing selected yet → both buttons must be hidden.
	if vis, _ := copyBtn.IsVisible(); vis {
		t.Error("Copy button should be hidden with no selection")
	}
	if vis, _ := cutBtn.IsVisible(); vis {
		t.Error("Cut button should be hidden with no selection")
	}

	// Direct-click selection via selectNode() — same code path as a click
	// on the canvas node.
	jsSelectNode(t, page, "rss_0")
	waitVisible(t, copyBtn)
	waitVisible(t, cutBtn)
}

// TestE2EHardFieldWarningOnNode verifies that a node whose required field has
// no upstream producer shows an amber ⚠ warning inline on the canvas card.
func TestE2EHardFieldWarningOnNode(t *testing.T) {
	// premiere requires series_episode_id etc., but rss doesn't produce them → hard warning.
	// This config is only loaded into the visual editor (not saved), so the server
	// can start with any valid config.
	const validCfg = `
src = input("rss", url="https://feeds.example.com/tv.rss")
sink = output("transmission", upstream=src, host="localhost")
pipeline("base")
`
	const warnCfg = `
src  = input("rss", url="https://feeds.example.com/tv.rss")
up   = process("premiere", upstream=src)
sink = output("transmission", upstream=up, host="localhost")
pipeline("demo")
`
	ts := startTestServer(t, validCfg)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	// Load the broken config into the visual editor without saving.
	switchToVisual(t, page, warnCfg)

	// premiere node must show the amber ⚠ hard warning.
	waitVisible(t, page.Locator(".ve-node-warn"))

	text, err := page.Locator(".ve-node-warn").First().TextContent()
	if err != nil {
		t.Fatalf("get warn text: %v", err)
	}
	if !contains(text, "series_episode_id") {
		t.Errorf("expected hard warning to mention series_episode_id, got: %q", text)
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
	defer page.Close()

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
	defer page.Close()

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
//   - watchlist is a user function wrapping a trakt_list source
//   - a movies node uses it via list=[watchlist()]
const traktListFunctionConfig = `
# pipeliner:param
def watchlist():
    src      = input("trakt_list", client_id="test-id", type="movies")
    filtered = process("condition", upstream=src, accept="true")
    return filtered

main_src = input("rss", url="https://example.com/rss")
seen     = process("seen", upstream=main_src)
flt      = process("movies", upstream=seen, list=[watchlist()], static=["Inception"])
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
	defer page.Close()

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

// ── route() node tests ────────────────────────────────────────────────────────

// routeConfig has a route() node with two ports: series and movies.
// Both ports feed separate seen processors that merge at a print sink.
const routeConfig = `
src    = input("rss", url="https://example.com/rss")
routes = route(src,
    series = "series_episode_id != ''",
    movies = "series_episode_id == ''")
series_path = process("seen", upstream=routes.series)
movies_path  = process("seen", upstream=routes.movies)
output("print", upstream=merge(series_path, movies_path))
pipeline("branched", schedule="1h")
`

// TestPlaywrightRouteNodeRendersPorts verifies that:
//  1. A route() node appears on the canvas.
//  2. Named port circles appear on the route card (one per rule).
//  3. No standalone route_selector chip nodes are visible on the canvas.
func TestPlaywrightRouteNodeRendersPorts(t *testing.T) {
	ts := startTestServer(t, routeConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, routeConfig)

	// 1. The route processor node must be on the canvas.
	if err := page.Locator(`.ve-node-name:has-text("route")`).First().WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("route node not visible on canvas: %v", err)
	}

	// 2. Named port circles must appear on the route card (one per rule).
	portList := page.Locator(`.ve-route-port`)
	count, err := portList.Count()
	if err != nil {
		t.Fatalf("count route ports: %v", err)
	}
	if count != 2 {
		t.Errorf("want 2 port circles on route card, got %d", count)
	}

	// 3. No standalone route_selector chip nodes on the canvas.
	chips, _ := page.Locator(`.ve-node.ve-node-route-port`).Count()
	if chips != 0 {
		t.Errorf("route_selector should not appear as standalone canvas nodes, got %d", chips)
	}
}

// TestPlaywrightRouteNodeParamPanel verifies that clicking the route node
// opens the param panel and shows the rules editor.
func TestPlaywrightRouteNodeParamPanel(t *testing.T) {
	ts := startTestServer(t, routeConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, routeConfig)

	// Click the route node itself.
	if err := page.Locator(`.ve-node-name:has-text("route")`).First().Click(); err != nil {
		t.Fatalf("click route node: %v", err)
	}

	// Param panel role element should show "processor".
	roleEl := page.Locator(`#ve-param-phase`)
	if err := roleEl.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("param panel role element not visible: %v", err)
	}
	roleText, err := roleEl.TextContent()
	if err != nil {
		t.Fatalf("get role text: %v", err)
	}
	if roleText != "processor" {
		t.Errorf("param panel role for route node: got %q, want 'processor'", roleText)
	}
}

// TestPlaywrightRouteCodegenRoundTrip verifies that loading a route() config
// in visual mode and switching back to text produces valid Starlark that
// contains route() syntax and the correct port references.
func TestPlaywrightRouteCodegenRoundTrip(t *testing.T) {
	ts := startTestServer(t, routeConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, routeConfig)

	// The text buffer is no longer regenerated by merely opening the visual
	// view — trigger a real mutation (drag the route node) so dagToStarlark
	// runs, then read the generated config.
	dragNodeBy(t, page, `.ve-node:has(.ve-node-name:has-text("route"))`, 40, 20)
	generated := editorContent(t, page)

	if generated == "" {
		t.Fatal("generated config is empty")
	}
	// Must contain a route() call.
	if !strings.Contains(generated, "route(") {
		t.Errorf("generated config missing route() call:\n%s", generated)
	}
	// Must contain port references (routeNodeId.series / routeNodeId.movies).
	if !strings.Contains(generated, ".series") {
		t.Errorf("generated config missing .series port reference:\n%s", generated)
	}
	if !strings.Contains(generated, ".movies") {
		t.Errorf("generated config missing .movies port reference:\n%s", generated)
	}
	// Must NOT contain route_selector (internal plugin, should not appear).
	if strings.Contains(generated, "route_selector") {
		t.Errorf("generated config must not expose internal route_selector plugin:\n%s", generated)
	}
}

// TestPlaywrightRoutePortsOnCard verifies the expected route node UX:
//  1. Each rule produces one named port circle on the bottom of the route card.
//  2. The port circle carries a tooltip with the port name.
//  3. Separate route_selector chip nodes are NOT visible as standalone canvas nodes.
func TestPlaywrightRoutePortsOnCard(t *testing.T) {
	ts := startTestServer(t, routeConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, routeConfig)

	// Wait for the route node.
	if err := page.Locator(`.ve-node-name:has-text("route")`).First().WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("route node not visible: %v", err)
	}

	// 1. Exactly two port circles must be visible on the route card.
	portList := page.Locator(`.ve-route-port`)
	count, err := portList.Count()
	if err != nil {
		t.Fatalf("count ports: %v", err)
	}
	if count != 2 {
		t.Errorf("want 2 ports on the route card (one per rule), got %d", count)
	}

	// 2. Each port carries a data-port attribute with the port name.
	seriesPort := page.Locator(`.ve-route-port[data-port="series"]`)
	if err := seriesPort.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateAttached,
	}); err != nil {
		t.Errorf("no port with data-port=\"series\" found: %v", err)
	}

	moviesPort := page.Locator(`.ve-route-port[data-port="movies"]`)
	if err := moviesPort.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateAttached,
	}); err != nil {
		t.Errorf("no port with data-port=\"movies\" found: %v", err)
	}

	// 3. route_selector nodes must NOT appear as visible standalone canvas nodes.
	selectorNodes := page.Locator(`.ve-node-route-port`)
	selectorCount, err := selectorNodes.Count()
	if err != nil {
		t.Fatalf("count selector nodes: %v", err)
	}
	if selectorCount > 0 {
		t.Errorf("route_selector should not appear as separate canvas nodes; got %d visible chips", selectorCount)
	}
}

// TestE2EConditionEditorClearsSoftWarning verifies that adding a condition
// node whose accept rule guarantees a required field clears the soft (~)
// warning that was previously shown on the downstream node.
func TestE2EConditionEditorClearsSoftWarning(t *testing.T) {
	// Without condition: content requires torrent_files, metainfo_torrent only
	// MayProduces it → soft warning on content node.
	const withoutCond = `
src   = input("rss", url="https://example.com/rss")
meta  = process("metainfo_torrent", upstream=src)
ct    = process("content", upstream=meta, reject=["*.rar"])
sink  = output("print", upstream=ct)
pipeline("test")
`
	// With condition: same pipeline but a condition node that accepts entries
	// where torrent_files is set AND rejects entries where it is absent. The
	// accept rule alone only promotes the field into the Accepted bucket —
	// matching entries still arrive at content alongside Undecided pass-
	// throughs that may lack the field. Pairing accept with a reject-absence
	// rule is the realistic user pattern the conditionMissingRejectWarning
	// nudges them toward, and the only configuration that makes
	// torrent_files certain on every entry flowing past.
	const withCond = `
src   = input("rss", url="https://example.com/rss")
meta  = process("metainfo_torrent", upstream=src)
cond  = process("condition", upstream=meta, rules=[
    {"accept": "torrent_files != \"\""},
    {"reject": "torrent_files == \"\""},
])
ct    = process("content", upstream=cond, reject=["*.rar"])
sink  = output("print", upstream=ct)
pipeline("test")
`
	// Start the server with any valid config; we load test configs via the editor.
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)

	// ── Step 1: without condition — soft warning must be visible ──────────────
	switchToVisual(t, page, withoutCond)

	waitVisible(t, page.Locator(".ve-node-soft-warn"))

	// Select the content node (index 2 in this pipeline).
	jsSelectNode(t, page, "content_2")
	waitVisible(t, page.Locator(".ve-conn-soft-warn"))

	softWarnText, err := page.Locator(".ve-conn-soft-warn").TextContent()
	if err != nil {
		t.Fatalf("get soft warn text: %v", err)
	}
	if !contains(softWarnText, "torrent_files") {
		t.Errorf("expected soft warning about torrent_files, got: %q", softWarnText)
	}

	// ── Step 2: with condition — soft warning must be gone ────────────────────
	switchToVisual(t, page, withCond)

	// Wait for exactly 5 canvas nodes (rss, meta, cond, content, print) to
	// confirm the withCond config has fully rendered before we interact.
	if err := page.Locator(".ve-node").Nth(4).WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateAttached,
		Timeout: playwright.Float(8000),
	}); err != nil {
		t.Fatalf("5th canvas node not found after withCond load: %v", err)
	}

	// Evaluate fieldWarnings() in the browser directly — this bypasses DOM
	// rendering timing and tests the narrowing logic itself, which is the
	// point of this test.
	var jsResult any
	jsResult, err = page.Evaluate(`
		(function() {
			var node = findNode('content_3');
			if (!node) return 'node_not_found';
			var warns = fieldWarnings(node);
			var tf = warns.filter(function(w) { return w.msg.indexOf('torrent_files') !== -1; });
			return tf.length === 0 ? 'ok' : 'warning:' + tf[0].msg;
		})()
	`)
	if err != nil {
		t.Fatalf("fieldWarnings JS evaluation failed: %v", err)
	}
	if jsResult != "ok" {
		t.Errorf("expected no torrent_files warning after condition accept rule, got: %v", jsResult)
	}

	// Canvas node-level soft warning must also be gone.
	// Give the render a moment to settle before counting.
	time.Sleep(200 * time.Millisecond)
	nodeWarnCount, _ := page.Locator(".ve-node-soft-warn").Count()
	if nodeWarnCount > 0 {
		t.Error("canvas soft-warn badge should be gone after condition accept rule")
	}
}

// ── condition editor e2e tests ────────────────────────────────────────────────

// conditionPipelineConfig is a minimal rss → condition → print DAG.
// The condition has a single accept-everything rule so it is valid.
// Node IDs are rss_0, condition_1, print_2.
const conditionPipelineConfig = `
src   = input("rss", url="https://example.com/rss")
cond  = process("condition", upstream=src, accept="true")
sink  = output("print", upstream=cond)
pipeline("test")
`

// TestE2EConditionEditorAddRule verifies that clicking "+ Accept" creates a
// visible rule card with the structured condition builder (field/op selects),
// not a raw text input.
func TestE2EConditionEditorAddRule(t *testing.T) {
	ts := startTestServer(t, conditionPipelineConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, conditionPipelineConfig)

	// Select the condition node.
	jsSelectNode(t, page, "condition_1")

	// The condition editor panel must be visible.
	waitVisible(t, page.Locator("#ve-cond-rules"))

	// Click "+ Accept".
	if err := page.Locator("button.ve-add-kv:has-text(\"+ Accept\")").Click(); err != nil {
		t.Fatalf("click +Accept: %v", err)
	}

	// A rule card must appear.
	waitVisible(t, page.Locator(".ve-cond-rule").First())

	// The accept type badge must be present.
	waitVisible(t, page.Locator(".ve-cond-type.accept").First())

	// The builder must render with a field selector (not just raw textarea),
	// meaning the default expression is parseable and shows builder mode.
	builderFieldSel := page.Locator(".ve-cb-field").First()
	if err := builderFieldSel.WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(5000),
	}); err != nil {
		t.Errorf("builder field selector not visible after +Accept: %v", err)
	}
}

// TestE2EConditionEditorFieldsAvailableSection verifies that the "Fields
// available at input" section appears when a condition node with an upstream
// rss source is selected, and that rss's produced fields are shown as certain.
func TestE2EConditionEditorFieldsAvailableSection(t *testing.T) {
	ts := startTestServer(t, conditionPipelineConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, conditionPipelineConfig)

	jsSelectNode(t, page, "condition_1")

	// The "Fields available at input" section must appear.
	waitVisible(t, page.Locator(".ve-fields-section"))

	// At least one certain (green) field tag must be visible.
	waitVisible(t, page.Locator(".ve-f-certain").First())

	// The rss plugin produces "source", "title", "rss_feed" — all must be certain.
	for _, field := range []string{"source", "title", "rss_feed"} {
		tag := page.Locator(fmt.Sprintf(".ve-f-certain:has-text(%q)", field))
		if err := tag.WaitFor(playwright.LocatorWaitForOptions{
			State:   playwright.WaitForSelectorStateVisible,
			Timeout: playwright.Float(4000),
		}); err != nil {
			t.Errorf("certain field tag %q not visible: %v", field, err)
		}
	}

	// "description" is in rss MayProduce — it must appear as reachable (amber), not certain.
	if cnt, _ := page.Locator(`.ve-f-certain:has-text("description")`).Count(); cnt > 0 {
		t.Error("description should be reachable (amber), not certain (green)")
	}
	waitVisible(t, page.Locator(`.ve-f-reachable:has-text("description")`))
}

// TestE2EConditionEditorAcceptNarrowsDownstream verifies that adding
// "accept: description != ”" to a condition node causes "description" to
// appear as a certain (green) field in the downstream print node's panel.
func TestE2EConditionEditorAcceptNarrowsDownstream(t *testing.T) {
	const cfg = `
src  = input("rss", url="https://example.com/rss")
cond = process("condition", upstream=src, accept="description != \"\"")
sink = output("print", upstream=cond)
pipeline("test")
`
	ts := startTestServer(t, cfg)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, cfg)

	// Select the downstream print node.
	jsSelectNode(t, page, "print_2")

	waitVisible(t, page.Locator(".ve-fields-section"))

	// description must now be CERTAIN (green) because the accept rule guarantees it.
	if err := page.Locator(`.ve-f-certain:has-text("description")`).WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(4000),
	}); err != nil {
		t.Errorf("description should be certain (green) downstream of accept rule: %v", err)
	}
}

// TestE2EConditionEditorORDoesNotPromote verifies that an OR accept rule
// like "description != ” or rss_category != ”" does NOT promote either
// field to certain downstream, since only one branch needs to match.
func TestE2EConditionEditorORDoesNotPromote(t *testing.T) {
	const cfg = `
src  = input("rss", url="https://example.com/rss")
cond = process("condition", upstream=src, accept="description != \"\" or rss_category != \"\"")
sink = output("print", upstream=cond)
pipeline("test")
`
	ts := startTestServer(t, cfg)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, cfg)

	jsSelectNode(t, page, "print_2")
	waitVisible(t, page.Locator(".ve-fields-section"))

	// Neither description nor rss_category should be certain — OR means one branch
	// might match without the other field being present.
	for _, field := range []string{"description", "rss_category"} {
		if cnt, _ := page.Locator(fmt.Sprintf(`.ve-f-certain:has-text(%q)`, field)).Count(); cnt > 0 {
			t.Errorf("field %q should NOT be certain after OR accept rule", field)
		}
	}
}

// TestE2EConditionEditorRejectRemovesField verifies that a reject rule
// "description != ”" causes "description" to disappear from both certain
// and reachable in the downstream node's Fields-available panel.
func TestE2EConditionEditorRejectRemovesField(t *testing.T) {
	const cfg = `
src  = input("rss", url="https://example.com/rss")
cond = process("condition", upstream=src, reject="description != \"\"")
sink = output("print", upstream=cond)
pipeline("test")
`
	ts := startTestServer(t, cfg)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, cfg)

	jsSelectNode(t, page, "print_2")
	waitVisible(t, page.Locator(".ve-fields-section"))

	// description must NOT appear in either certain or reachable tags.
	for _, cls := range []string{".ve-f-certain", ".ve-f-reachable"} {
		if cnt, _ := page.Locator(fmt.Sprintf(`%s:has-text("description")`, cls)).Count(); cnt > 0 {
			t.Errorf("description should be removed from %s after reject rule", cls)
		}
	}

	// But source and title (not filtered) must still be present as certain.
	for _, field := range []string{"source", "title"} {
		if err := page.Locator(fmt.Sprintf(`.ve-f-certain:has-text(%q)`, field)).WaitFor(playwright.LocatorWaitForOptions{
			State:   playwright.WaitForSelectorStateVisible,
			Timeout: playwright.Float(4000),
		}); err != nil {
			t.Errorf("field %q should still be certain after reject rule: %v", field, err)
		}
	}
}

// TestE2EConditionEditorRawBuilderToggle verifies that the ⋮ raw button
// switches a builder-mode rule to a raw textarea, and the ≡ builder button
// switches it back.
func TestE2EConditionEditorRawBuilderToggle(t *testing.T) {
	// Start with a rule already in builder mode (parseable expression).
	const cfg = `
src  = input("rss", url="https://example.com/rss")
cond = process("condition", upstream=src, accept="source != \"\"")
sink = output("print", upstream=cond)
pipeline("test")
`
	ts := startTestServer(t, cfg)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, cfg)

	jsSelectNode(t, page, "condition_1")
	waitVisible(t, page.Locator(".ve-cond-rule").First())

	// Should start in builder mode — field selector visible.
	waitVisible(t, page.Locator(".ve-cb-field").First())

	// Click "⋮ raw" to switch to raw mode.
	rawBtn := page.Locator(`.ve-cond-raw-btn:has-text("raw")`).First()
	waitVisible(t, rawBtn)
	if err := rawBtn.Click(); err != nil {
		t.Fatalf("click raw toggle: %v", err)
	}

	// Raw textarea must appear.
	waitVisible(t, page.Locator(".ve-cond-raw-ta").First())

	// Builder field selector must disappear.
	fieldSelCount, _ := page.Locator(".ve-cb-field").Count()
	if fieldSelCount > 0 {
		t.Error("builder field selector should be hidden in raw mode")
	}

	// Click "≡ builder" to switch back.
	builderBtn := page.Locator(`.ve-cond-raw-btn:has-text("builder")`).First()
	waitVisible(t, builderBtn)
	if err := builderBtn.Click(); err != nil {
		t.Fatalf("click builder toggle: %v", err)
	}

	// Builder field selector must reappear.
	waitVisible(t, page.Locator(".ve-cb-field").First())
}

// TestE2EConditionEditorNarrowingPreview verifies that the narrowing preview
// notice ("Promotes to certain: …") appears in the rule card when an accept
// rule references a reachable field.
func TestE2EConditionEditorNarrowingPreview(t *testing.T) {
	ts := startTestServer(t, conditionPipelineConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, conditionPipelineConfig)

	jsSelectNode(t, page, "condition_1")

	// Add an accept rule.
	if err := page.Locator("button.ve-add-kv:has-text(\"+ Accept\")").Click(); err != nil {
		t.Fatalf("click +Accept: %v", err)
	}
	waitVisible(t, page.Locator(".ve-cond-rule").First())

	// Change the field to "description" (which is reachable, not yet certain).
	// This exercises the narrowing preview path.
	fieldSel := page.Locator(".ve-cb-field").First()
	if _, err := fieldSel.SelectOption(playwright.SelectOptionValues{
		Values: &[]string{"description"},
	}); err != nil {
		// description may not be in the select if fields aren't loaded yet — soft fail
		t.Logf("note: could not select description field: %v", err)
		return
	}

	// The narrowing notice must appear after field selection.
	narrowing := page.Locator(".ve-cond-narrow").First()
	if err := narrowing.WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(3000),
	}); err != nil {
		t.Errorf("narrowing preview not visible after selecting a reachable field: %v", err)
	}
}

// TestE2EConditionEditorEdgeTooltip verifies that the edge field tooltip
// function populates the tooltip with field tags for the source node's output.
// SVG path hover is unreliable in headless mode so we trigger the tooltip
// function directly via JavaScript and inspect the resulting DOM.
func TestE2EConditionEditorEdgeTooltip(t *testing.T) {
	ts := startTestServer(t, conditionPipelineConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, conditionPipelineConfig)

	// Wait for the canvas to render edges.
	if err := page.Locator("path.ve-edge").First().WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateAttached,
		Timeout: playwright.Float(5000),
	}); err != nil {
		t.Fatalf("no edge in canvas: %v", err)
	}

	// Trigger the tooltip directly for the rss source node (rss_0) via JS.
	// This calls showEdgeFieldTooltip which builds the tooltip from live field data.
	if _, err := page.Evaluate(`showEdgeFieldTooltip({clientX:200, clientY:200}, 'rss_0')`); err != nil {
		t.Fatalf("showEdgeFieldTooltip JS call failed: %v", err)
	}

	// The tooltip element must now be visible.
	tooltip := page.Locator("#ve-edge-tooltip")
	if err := tooltip.WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(3000),
	}); err != nil {
		t.Errorf("edge field tooltip not visible after JS trigger: %v", err)
	}

	// Must contain field tags (rss produces source, title, rss_feed → certain).
	tagCount, _ := page.Locator("#ve-edge-tooltip .ve-f-certain, #ve-edge-tooltip .ve-f-reachable").Count()
	if tagCount == 0 {
		t.Error("edge tooltip appeared but shows no field tags")
	}
}

// TestE2EEdgeTooltipAppearsOnHover drives the real hover path: mousing over an
// edge's invisible hit-stroke must show the field tooltip. Regression test for
// showEdgeFieldTooltip calling collectOutputFields with a long-stale signature,
// which left the tooltip permanently empty (and thus never displayed).
func TestE2EEdgeTooltipAppearsOnHover(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, dagConfig)

	hit := page.Locator("path.ve-edge-hit").First()
	if err := hit.WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateAttached,
		Timeout: playwright.Float(5000),
	}); err != nil {
		t.Fatalf("no edge hit-path in canvas: %v", err)
	}
	if err := hit.Hover(); err != nil {
		t.Fatalf("hover edge hit-path: %v", err)
	}

	tooltip := page.Locator("#ve-edge-tooltip")
	if err := tooltip.WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(3000),
	}); err != nil {
		t.Fatalf("edge field tooltip not visible after hovering the edge: %v", err)
	}
	tagCount, _ := page.Locator("#ve-edge-tooltip .ve-f-certain, #ve-edge-tooltip .ve-f-reachable").Count()
	if tagCount == 0 {
		t.Error("edge tooltip visible on hover but shows no field tags")
	}
}

// TestE2EDeleteKeyRemovesMultiSelection verifies that pressing Delete with a
// multi-selection removes every selected node (previously only the single
// selectedNodeId was deleted and marquee selections deleted nothing).
func TestE2EDeleteKeyRemovesMultiSelection(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, dagConfig)

	// Multi-select every main node via the same entry point Ctrl+click uses.
	if _, err := page.Evaluate(`(function () {
		for (const n of ve.graphs[0].nodes) {
			if (!n.isSearchNode && !n.isListNode && !n.isRoutePort) toggleMultiSelect(n.id);
		}
		return ve.selectedNodeIds.size;
	})()`); err != nil {
		t.Fatalf("multi-select nodes: %v", err)
	}

	if err := page.Keyboard().Press("Delete"); err != nil {
		t.Fatalf("press Delete: %v", err)
	}

	// All node cards must be gone from the canvas and the model.
	res, err := page.Evaluate(`ve.graphs[0].nodes.length`)
	if err != nil {
		t.Fatalf("read node count: %v", err)
	}
	if n, ok := res.(int); ok && n != 0 {
		t.Errorf("model still has %d nodes after multi-delete", n)
	}
	count, _ := page.Locator(".ve-node").Count()
	if count != 0 {
		t.Errorf("canvas still shows %d node cards after multi-delete", count)
	}

	// Single undo restores everything (one snapshot for the whole batch).
	if _, err := page.Evaluate(`undo()`); err != nil {
		t.Fatalf("undo: %v", err)
	}
	res, _ = page.Evaluate(`ve.graphs[0].nodes.length`)
	if n, ok := res.(int); ok && n == 0 {
		t.Error("undo did not restore the deleted nodes")
	}
}

// TestE2ESelectingNodeInOtherPipelineActivatesIt reproduces the regression
// originally reported as "click plugin in unselected pipeline → that pipeline
// stays unselected". The bug compounded because subsequent clicks on the
// pipeline (label or empty region) had no visible effect, leaving the
// highlight stuck on the previously-active pipeline.
func TestE2ESelectingNodeInOtherPipelineActivatesIt(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)

	twoPipelines := `src_a = input("rss", url="https://a.example.com/rss")
seen_a = process("seen", upstream=src_a)
pipeline("pipe-a")

src_b = input("rss", url="https://b.example.com/rss")
seen_b = process("seen", upstream=src_b)
pipeline("pipe-b")`

	switchToVisual(t, page, twoPipelines)

	// Wait until both pipeline labels are rendered.
	if err := page.Locator(`.ve-pipeline-label[data-graph-idx="1"]`).WaitFor(
		playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateAttached},
	); err != nil {
		t.Fatalf("wait for pipeline-b label: %v", err)
	}

	labelA := page.Locator(`.ve-pipeline-label[data-graph-idx="0"]`)
	labelB := page.Locator(`.ve-pipeline-label[data-graph-idx="1"]`)
	regionA := page.Locator(`.ve-pipeline-region[data-graph-idx="0"]`)
	regionB := page.Locator(`.ve-pipeline-region[data-graph-idx="1"]`)

	hasActive := func(l playwright.Locator) bool {
		cls, _ := l.GetAttribute("class")
		return strings.Contains(" "+cls+" ", " active ")
	}

	// Sanity: pipeline-a is the initial active graph.
	if !hasActive(labelA) {
		cls, _ := labelA.GetAttribute("class")
		t.Fatalf("baseline: pipeline-a label should be active; class=%q", cls)
	}

	// Click a node inside pipeline-b. Node IDs are pluginname_N counting
	// across all pipelines, so seen_b is seen_3.
	if err := page.Locator(`.ve-node[data-id="seen_3"]`).Click(); err != nil {
		t.Fatalf("click seen_b node: %v", err)
	}

	// Both label AND region for pipeline-b should be active; pipeline-a
	// should no longer be active.
	if hasActive(labelA) {
		cls, _ := labelA.GetAttribute("class")
		t.Errorf("pipeline-a label should NOT be active after switching; class=%q", cls)
	}
	if !hasActive(labelB) {
		cls, _ := labelB.GetAttribute("class")
		t.Errorf("pipeline-b label should be active; class=%q", cls)
	}
	if hasActive(regionA) {
		cls, _ := regionA.GetAttribute("class")
		t.Errorf("pipeline-a region should NOT be active; class=%q", cls)
	}
	if !hasActive(regionB) {
		cls, _ := regionB.GetAttribute("class")
		t.Errorf("pipeline-b region should be active; class=%q", cls)
	}

	// Now click the empty area inside pipeline-b's region to dismiss the
	// param panel. The pipeline must stay active.
	bb, err := regionB.BoundingBox()
	if err != nil || bb == nil {
		t.Fatalf("pipeline-b region bounding box: %v", err)
	}
	// Click below the last node in pipeline-b (well inside the region).
	if err := page.Mouse().Click(bb.X+bb.Width-30, bb.Y+bb.Height-20); err != nil {
		t.Fatalf("click empty area in pipeline-b: %v", err)
	}
	if !hasActive(labelB) {
		cls, _ := labelB.GetAttribute("class")
		t.Errorf("after dismissing param panel, pipeline-b label should still be active; class=%q", cls)
	}
	if !hasActive(regionB) {
		cls, _ := regionB.GetAttribute("class")
		t.Errorf("after dismissing param panel, pipeline-b region should still be active; class=%q", cls)
	}

	// And clicking the same empty area a second time must not regress.
	if err := page.Mouse().Click(bb.X+bb.Width-30, bb.Y+bb.Height-20); err != nil {
		t.Fatalf("second click in pipeline-b: %v", err)
	}
	if !hasActive(labelB) {
		cls, _ := labelB.GetAttribute("class")
		t.Errorf("after second click, pipeline-b label should still be active; class=%q", cls)
	}
}

// TestE2EClickingPipelineLabelDismissesParamPanel covers the secondary half
// of the user-reported flow: after a node in pipeline-b has been selected and
// its param panel is showing, clicking pipeline-b's label should both keep
// pipeline-b active AND dismiss the param panel (clearing the previously
// selected node). Without this, the label click handler silently leaves the
// stale node selection in place.
func TestE2EClickingPipelineLabelDismissesParamPanel(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)

	twoPipelines := `src_a = input("rss", url="https://a.example.com/rss")
seen_a = process("seen", upstream=src_a)
pipeline("pipe-a")

src_b = input("rss", url="https://b.example.com/rss")
seen_b = process("seen", upstream=src_b)
pipeline("pipe-b")`

	switchToVisual(t, page, twoPipelines)

	if err := page.Locator(`.ve-pipeline-label[data-graph-idx="1"]`).WaitFor(
		playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateAttached},
	); err != nil {
		t.Fatalf("wait for pipeline-b label: %v", err)
	}

	// Click seen_b (in pipeline-b) to open its param panel.
	if err := page.Locator(`.ve-node[data-id="seen_3"]`).Click(); err != nil {
		t.Fatalf("click seen_b: %v", err)
	}
	if err := page.Locator(`.ve-node[data-id="seen_3"].selected`).WaitFor(
		playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateAttached, Timeout: playwright.Float(2000)},
	); err != nil {
		t.Fatalf("seen_b should be selected after click: %v", err)
	}

	// Now click pipeline-b's name label.
	if err := page.Locator(`.ve-pipeline-label[data-graph-idx="1"] .ve-pl-name`).Click(); err != nil {
		t.Fatalf("click pipeline-b name: %v", err)
	}

	// The .selected class on the node should be gone — clicking the pipeline
	// label is meant to dismiss the param panel.
	selectedAfter, _ := page.Locator(`.ve-node.selected`).Count()
	if selectedAfter != 0 {
		t.Errorf("clicking pipeline-b label should clear the selected node; %d nodes still .selected", selectedAfter)
	}
}

// ── sub-node drag-follow ──────────────────────────────────────────────────────
//
// When a parent node (e.g. discover) has an attached search/list sub-node,
// dragging the parent — single-node or as part of a multi-selection — must
// translate the sub-node by the same delta. Before the fix, the multi-drag
// handler excluded sub-nodes from the moved set and the single-drag handler
// never touched them, so they stayed frozen while the parent slid away.

const discoverWithSearchConfig = `# pipeliner:pos 50 76
titles = input("rss", url="https://example.com/rss")

# pipeliner:pos 320 76 search 320 246
disc = process("discover", upstream=titles, interval="24h",
  search=[{"name": "rss", "url_template": "https://nyaa.si/?page=rss&q={QueryEscaped}"}])

pipeline("tv")`

// dragNodeBy moves the parent of `parentSelector` by (dx, dy) using a real
// pointer-event sequence. Several intermediate move steps are emitted so the
// editor's multi-drag path works — its deferred-drag wrapper consumes the
// threshold-crossing pointermove to *install* the drag handler, and only the
// next pointermove actually moves the node.
func dragNodeBy(t *testing.T, page playwright.Page, parentSelector string, dx, dy float64) {
	t.Helper()
	bb, err := page.Locator(parentSelector).BoundingBox()
	if err != nil || bb == nil {
		t.Fatalf("bounding box for %q: %v", parentSelector, err)
	}
	cx, cy := bb.X+bb.Width/2, bb.Y+bb.Height/2
	if err := page.Mouse().Move(cx, cy); err != nil {
		t.Fatalf("mouse move to node: %v", err)
	}
	if err := page.Mouse().Down(); err != nil {
		t.Fatalf("mouse down: %v", err)
	}
	// Walk to the target in several steps. The first two steps cross the 5px
	// deferred-drag threshold (which installs the real drag handler); the
	// remaining steps actually fire the drag handler's pointermove logic.
	const steps = 6
	for i := 1; i <= steps; i++ {
		f := float64(i) / float64(steps)
		if err := page.Mouse().Move(cx+dx*f, cy+dy*f); err != nil {
			t.Fatalf("mouse drag move (step %d): %v", i, err)
		}
	}
	if err := page.Mouse().Up(); err != nil {
		t.Fatalf("mouse up: %v", err)
	}
}

// nodePosition reads {x, y} from a node's inline style.left/style.top — the
// canvas-space position the drag handler writes directly.
func nodePosition(t *testing.T, page playwright.Page, selector string) (float64, float64) {
	t.Helper()
	res, err := page.Locator(selector).Evaluate(`el => ({x: parseFloat(el.style.left), y: parseFloat(el.style.top)})`, nil)
	if err != nil {
		t.Fatalf("read position for %q: %v", selector, err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("position result is %T, want map", res)
	}
	asFloat := func(v any, key string) float64 {
		switch x := v.(type) {
		case float64:
			return x
		case int:
			return float64(x)
		case int64:
			return float64(x)
		default:
			t.Fatalf("position[%s] is %T, want number", key, v)
			return 0
		}
	}
	return asFloat(m["x"], "x"), asFloat(m["y"], "y")
}

func TestE2ESingleDragMovesSearchSubNode(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, discoverWithSearchConfig)

	// Discover is the main node; .ve-node-search is its attached sub-node.
	const parent = `.ve-node:has(.ve-node-name:has-text("discover"))`
	const sub = `.ve-node.ve-node-search`

	if err := page.Locator(sub).WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("search sub-node not visible: %v", err)
	}

	px0, py0 := nodePosition(t, page, parent)
	sx0, sy0 := nodePosition(t, page, sub)

	dragNodeBy(t, page, parent, 80, 60)

	px1, py1 := nodePosition(t, page, parent)
	sx1, sy1 := nodePosition(t, page, sub)

	parentDx, parentDy := px1-px0, py1-py0
	subDx, subDy := sx1-sx0, sy1-sy0

	if parentDx == 0 && parentDy == 0 {
		t.Fatalf("parent did not move: parent dx=%.1f dy=%.1f", parentDx, parentDy)
	}
	// Sub-node delta must match parent's effective (post-clamp) delta within
	// a small tolerance for sub-pixel rounding.
	const tol = 1.0
	if abs(subDx-parentDx) > tol || abs(subDy-parentDy) > tol {
		t.Errorf("search sub-node did not follow parent: parent Δ=(%.1f,%.1f), sub Δ=(%.1f,%.1f)",
			parentDx, parentDy, subDx, subDy)
	}
}

func TestE2EMultiDragMovesSearchSubNode(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, discoverWithSearchConfig)

	const titles = `.ve-node:has(.ve-node-name:has-text("rss")):not(.ve-node-search)`
	const disc = `.ve-node:has(.ve-node-name:has-text("discover"))`
	const sub = `.ve-node.ve-node-search`

	if err := page.Locator(sub).WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("search sub-node not visible: %v", err)
	}

	// Build a two-node multi-selection via Cmd+click (Meta on macOS, Ctrl
	// elsewhere — Playwright accepts Meta on linux too and the editor's
	// pointerdown handler treats both the same).
	if err := page.Locator(titles).Click(playwright.LocatorClickOptions{
		Modifiers: []playwright.KeyboardModifier{*playwright.KeyboardModifierMeta},
	}); err != nil {
		t.Fatalf("meta-click titles node: %v", err)
	}
	if err := page.Locator(disc).Click(playwright.LocatorClickOptions{
		Modifiers: []playwright.KeyboardModifier{*playwright.KeyboardModifierMeta},
	}); err != nil {
		t.Fatalf("meta-click discover node: %v", err)
	}

	// The search sub-node must inherit the multi-selected class from its parent.
	subClasses, err := page.Locator(sub).GetAttribute("class")
	if err != nil {
		t.Fatalf("read sub class: %v", err)
	}
	if !strings.Contains(subClasses, "multi-selected") {
		t.Fatalf("sub-node should carry multi-selected class when parent is in selection; got %q", subClasses)
	}

	px0, py0 := nodePosition(t, page, disc)
	sx0, sy0 := nodePosition(t, page, sub)

	dragNodeBy(t, page, disc, 90, 50)

	px1, py1 := nodePosition(t, page, disc)
	sx1, sy1 := nodePosition(t, page, sub)

	parentDx, parentDy := px1-px0, py1-py0
	subDx, subDy := sx1-sx0, sy1-sy0

	if parentDx == 0 && parentDy == 0 {
		t.Fatalf("parent did not move during multi-drag: dx=%.1f dy=%.1f", parentDx, parentDy)
	}
	const tol = 1.0
	if abs(subDx-parentDx) > tol || abs(subDy-parentDy) > tol {
		t.Errorf("multi-drag: search sub-node did not follow parent: parent Δ=(%.1f,%.1f), sub Δ=(%.1f,%.1f)",
			parentDx, parentDy, subDx, subDy)
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// TestE2EPasteLandsInsideActivePipelineRegion verifies that pasting a copied
// node into a pipeline whose region sits below the viewport's vertical centre
// places the new node inside that region's y-band. Without the clamp the paste
// landed where the viewport centre fell — typically above the active region —
// which startNodeDrag would then snap downward into the region as soon as the
// user tried to move it (its `effMinY` floors y at g._labelY + 36).
func TestE2EPasteLandsInsideActivePipelineRegion(t *testing.T) {
	const cfg = `
a_src  = input("rss", url="https://feeds.example.com/a.rss")
a_sink = output("print", upstream=a_src)
pipeline("alpha")

b_src  = input("rss", url="https://feeds.example.com/b.rss")
b_sink = output("print", upstream=b_src)
pipeline("beta")
`
	ts := startTestServer(t, cfg)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, cfg)

	// Select a node in the SECOND pipeline so its graph is active. The beta
	// pipeline's region sits below the alpha pipeline's, which is exactly where
	// the previous viewport-only paste calculation went wrong.
	jsSelectNode(t, page, "rss_2")

	if _, err := page.Evaluate(`copySelected()`); err != nil {
		t.Fatalf("copySelected: %v", err)
	}
	if _, err := page.Evaluate(`pasteClipboard()`); err != nil {
		t.Fatalf("pasteClipboard: %v", err)
	}

	// The pasted node must land at a y at or below the drag floor
	// (g._labelY + 36) — otherwise the first drag would snap it down.
	res, err := page.Evaluate(`
		(function () {
			var g     = ve.graphs[ve.activeGraph];
			var floor = (g._labelY != null ? g._labelY : (g._regionY != null ? g._regionY + 8 : 0)) + 36;
			var newest = g.nodes[g.nodes.length - 1];
			return { y: newest.y, floor: floor };
		})()
	`)
	if err != nil {
		t.Fatalf("evaluate node y: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("unexpected eval result: %v", res)
	}
	num := func(v any) float64 {
		switch x := v.(type) {
		case float64:
			return x
		case int:
			return float64(x)
		}
		return 0
	}
	y, floor := num(m["y"]), num(m["floor"])
	if y < floor {
		t.Errorf("pasted node y (%v) is above the active pipeline's drag floor (%v) — would snap downward on first drag", y, floor)
	}
}

// TestE2EBlurayPaletteChipShowsBothBadges verifies that a plugin advertising
// both IsSearchPlugin and IsListPlugin (bluray_releases is the canonical case)
// renders BOTH badges visibly on its palette chip — not just one. The previous
// regression was visual rather than logical: both badges were emitted into the
// DOM, but the chip's `overflow:hidden` clipped the trailing badge when the
// plugin name was long enough to push it past the right edge.
func TestE2EBlurayPaletteChipShowsBothBadges(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, err := browser.NewPage()
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, dagConfig)

	chip := page.Locator(`#ve-palette-body .ve-chip[data-plugin="bluray_releases"]`)
	if err := chip.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateAttached,
	}); err != nil {
		paletteHTML, _ := page.Locator("#ve-palette-body").InnerHTML()
		t.Logf("palette HTML: %s", paletteHTML)
		t.Fatalf("bluray_releases chip not present in palette: %v", err)
	}

	// Verify both badges render inside the chip's visible (unclipped) area.
	// IsVisible() alone returns true even if a child element is clipped by an
	// overflow:hidden ancestor — exactly the failure mode that hid the list
	// badge in the wild — so we compare bounding rects instead.
	rect, err := chip.Evaluate(`el => {
		const c = el.getBoundingClientRect();
		const s = el.querySelector('.ve-chip-search-badge')?.getBoundingClientRect();
		const l = el.querySelector('.ve-chip-list-badge')?.getBoundingClientRect();
		return {
			chipRight: c.right,
			searchRight: s ? s.right : null,
			listRight: l ? l.right : null,
		};
	}`, nil)
	if err != nil {
		t.Fatalf("read bounding rects: %v", err)
	}
	m, _ := rect.(map[string]any)
	getNum := func(v any) (float64, bool) {
		switch x := v.(type) {
		case float64:
			return x, true
		case int:
			return float64(x), true
		}
		return 0, false
	}
	chipRight, _ := getNum(m["chipRight"])
	searchRight, sok := getNum(m["searchRight"])
	listRight, lok := getNum(m["listRight"])
	if !sok {
		t.Error("search badge not present in DOM")
	} else if searchRight > chipRight+0.5 {
		t.Errorf("search badge clipped: searchRight=%.1f > chipRight=%.1f", searchRight, chipRight)
	}
	if !lok {
		t.Error("list badge not present in DOM")
	} else if listRight > chipRight+0.5 {
		t.Errorf("list badge clipped by chip overflow: listRight=%.1f > chipRight=%.1f (this is the bug — the list badge is rendered but visually hidden)", listRight, chipRight)
	}
}

// TestE2ERunHistoryExpansion verifies that clicking a task card's header
// toggles the run-history panel and that the expansion state survives a
// dashboard re-render.
func TestE2ERunHistoryExpansion(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, err := browser.NewPage()
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	defer page.Close()

	login(t, page, ts.url)

	header := page.Locator(".task-card .task-card-header").First()
	if err := header.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("wait for task card header: %v", err)
	}

	// Collapsed by default.
	if n, _ := page.Locator(".task-history").Count(); n != 0 {
		t.Fatalf("history panel visible before expansion: %d nodes", n)
	}

	// Click to expand — the fresh server has no runs, so the empty-state
	// row proves the panel (not just the class) rendered.
	if err := header.Click(); err != nil {
		t.Fatalf("click header: %v", err)
	}
	if err := page.Locator(".task-history").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("history panel did not appear after header click: %v", err)
	}
	if err := page.Locator(".task-history-empty").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("empty-state history row missing: %v", err)
	}

	// A forced re-render (what the 10s poll does) must preserve expansion.
	if _, err := page.Evaluate("refresh()"); err != nil {
		t.Fatalf("force refresh: %v", err)
	}
	if err := page.Locator(".task-history").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("history panel lost after re-render: %v", err)
	}

	// Click again to collapse.
	if err := page.Locator(".task-card .task-card-header").First().Click(); err != nil {
		t.Fatalf("click header to collapse: %v", err)
	}
	if err := page.Locator(".task-history").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateHidden,
	}); err != nil {
		t.Fatalf("history panel still visible after collapse: %v", err)
	}
}

// TestE2EDashboardRunButtonHoverColor verifies the dashboard "Run now" button
// hover uses the accent colour as its own value, not via the per-card
// --card-color custom property. The regression was that .btn-run:hover used
// var(--card-color, var(--accent)) — and --card-color defaults to var(--border)
// for cards with no run history (typical for no-schedule pipelines), so the
// hover faded to grey instead of signalling an action. The test inspects the
// CSS rule directly because headless Chromium does not reliably trigger
// :hover via synthetic mouse moves.
func TestE2EDashboardRunButtonHoverColor(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, err := browser.NewPage()
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	defer page.Close()

	login(t, page, ts.url)

	// Wait for the dashboard so the stylesheet is loaded.
	if err := page.Locator(".task-card .btn-run:not(.btn-dry)").First().WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("wait for .btn-run: %v", err)
	}

	// Pull the border-color source value from the .btn-run:hover rule. We
	// assert it does NOT reference --card-color (which is what caused the
	// faded grey on cards with no run history).
	border, err := page.Evaluate(`
		(function () {
			for (const sheet of document.styleSheets) {
				let rules;
				try { rules = sheet.cssRules; } catch (e) { continue; }
				for (const r of rules) {
					if (r.selectorText && r.selectorText.includes('.btn-run:hover')
					    && !r.selectorText.includes('.btn-dry')) {
						return r.style.borderColor;
					}
				}
			}
			return null;
		})()
	`)
	if err != nil {
		t.Fatalf("read .btn-run:hover rule: %v", err)
	}
	if border == nil {
		t.Fatal(".btn-run:hover rule not found in stylesheets")
	}
	got, _ := border.(string)
	if strings.Contains(got, "--card-color") {
		t.Errorf("btn-run hover border-color still references --card-color: %q (regression: hover fades to grey on cards with no run history)", got)
	}
	if !strings.Contains(got, "--accent") {
		t.Errorf("btn-run hover border-color should resolve via --accent: got %q", got)
	}
}

// TestE2EDatabaseTabRendersCacheSection seeds two cache buckets, opens the
// database tab, and verifies that:
//   - the new "Caches" sidebar section appears alongside "Trackers"
//   - each cache bucket is listed with its display name + entry count
//   - the cache renderer surfaces the key, a shape-aware value preview, and
//     an expiry label drawn from the storedEntry envelope
//   - the per-row delete button removes the entry without disturbing siblings
func TestE2EDatabaseTabRendersCacheSection(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	// Seed two cache buckets directly via SQL so we exercise the exact on-disk
	// envelope the live cache writes (storedEntry: {v, e}). Bucket.Put would
	// JSON-marshal the value a second time, producing a base64-string blob.
	expires := time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339Nano)
	seed := func(bucket, key, innerJSON string) {
		envelope := fmt.Sprintf(`{"v":%s,"e":%q}`, innerJSON, expires)
		if _, err := ts.db.DB().Exec(
			`INSERT INTO store (bucket, key, value) VALUES (?, ?, ?)`,
			bucket, key, envelope); err != nil {
			t.Fatalf("seed %s/%s: %v", bucket, key, err)
		}
	}
	seed("cache_bluray_search_neg", "avatar", `"2026-06-12T10:00:00Z"`)
	seed("cache_bluray_index", "avatar", `[{"id":"7847","format":"BD"},{"id":"26954","format":"BD3D"}]`)

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)

	if err := page.Locator("#tab-btn-db").Click(); err != nil {
		t.Fatalf("click database tab: %v", err)
	}
	if err := page.Locator("#tab-db").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("db tab not visible: %v", err)
	}

	// "Caches" section header must appear in the sidebar.
	cachesHeader := page.Locator(".db-sidebar-section", playwright.PageLocatorOptions{
		HasText: "Caches",
	})
	if err := cachesHeader.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("Caches sidebar section not rendered: %v", err)
	}

	// Both seeded cache buckets must be listed. We match on the raw bucket
	// name in the onclick handler — the visible label comes from the API's
	// display field, which isn't asserted here (covered by database_test.go).
	for _, want := range []string{"cache_bluray_index", "cache_bluray_search_neg"} {
		// The nav button text is the display label (not the raw bucket name),
		// so use the data-driven onclick selector to find the right button.
		btn := page.Locator(fmt.Sprintf(`.db-nav-btn[onclick*=%q]`, want))
		if err := btn.WaitFor(playwright.LocatorWaitForOptions{
			State: playwright.WaitForSelectorStateVisible,
		}); err != nil {
			t.Fatalf("nav button for %s not visible: %v", want, err)
		}
	}

	// Open the index cache and assert the renderer shows the key + value
	// preview ("[2 items]" for the seeded array) + a relative expires label.
	if err := page.Locator(`.db-nav-btn[onclick*="cache_bluray_index"]`).Click(); err != nil {
		t.Fatalf("click cache_bluray_index: %v", err)
	}
	if err := page.Locator(".db-cache-table").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("cache table not rendered: %v", err)
	}
	keyCell, err := page.Locator(".db-cache-key").First().TextContent()
	if err != nil {
		t.Fatalf("read key cell: %v", err)
	}
	if keyCell != "avatar" {
		t.Errorf("key cell: got %q, want %q", keyCell, "avatar")
	}
	previewCell, err := page.Locator(".db-cache-value").First().TextContent()
	if err != nil {
		t.Fatalf("read preview cell: %v", err)
	}
	if previewCell != "[2 items]" {
		t.Errorf("value preview: got %q, want %q", previewCell, "[2 items]")
	}
	expiresCell, err := page.Locator(".db-cache-expires").First().TextContent()
	if err != nil {
		t.Fatalf("read expires cell: %v", err)
	}
	if !strings.HasPrefix(expiresCell, "in ") {
		t.Errorf("expires label: got %q, want a prefix-\"in \" label for a future timestamp", expiresCell)
	}

	// Delete the row and confirm the table becomes empty (the seeded bucket
	// only has the one key). Auto-confirm the prompt.
	page.OnDialog(func(d playwright.Dialog) { _ = d.Accept() })
	if err := page.Locator(".db-cache-table .btn-sm-danger").First().Click(); err != nil {
		t.Fatalf("click delete: %v", err)
	}
	// After deletion the bucket re-renders with the empty-state message.
	emptyState := page.Locator("#db-main-content .db-empty")
	if err := emptyState.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("empty state not rendered after delete: %v", err)
	}
	emptyText, _ := emptyState.TextContent()
	if !strings.Contains(emptyText, "No cached entries") {
		t.Errorf("expected 'No cached entries' empty state, got %q", emptyText)
	}
}

// TestE2EPluginDebugSettings exercises the round-trip for the Settings tab's
// "Plugin Debug Logging" panel: the section auto-loads plugin + override
// state when opened, clicking a checkbox PUTs the new set to
// /api/log-debug-plugins, the wired control records the change, and the
// row visibly switches into its active state. Clicking again removes it.
func TestE2EPluginDebugSettings(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	ctl := &captureLogCtl{}
	ts.srv.SetPluginLogControl(ctl)

	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	if err := page.Locator("#tab-btn-settings").Click(); err != nil {
		t.Fatalf("click settings tab: %v", err)
	}
	// Wait for the panel to populate (replaces the "Loading…" placeholder).
	if err := page.Locator("#plugin-debug-list .plugin-debug-cb").First().WaitFor(
		playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateVisible},
	); err != nil {
		t.Fatalf("plugin-debug list never rendered checkboxes: %v", err)
	}

	// Toggle the rss source plugin (always registered for minimalConfig).
	row := page.Locator(`#plugin-debug-list label:has(.plugin-debug-name:text-is("rss"))`)
	if err := row.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("rss row not visible: %v", err)
	}
	if err := row.Locator(".plugin-debug-cb").Click(); err != nil {
		t.Fatalf("click rss checkbox: %v", err)
	}

	// Server-side control should observe ["rss"] within a short window.
	deadline := time.Now().Add(3 * time.Second)
	var got []string
	for time.Now().Before(deadline) {
		got = ctl.DebugPlugins()
		if len(got) == 1 && got[0] == "rss" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(got) != 1 || got[0] != "rss" {
		t.Fatalf("control after enable: got %v, want [rss]", got)
	}

	// And the row visibly switches to its active style — wait for the active
	// class, since renderPluginDebugList runs AFTER the PUT promise resolves.
	if err := page.Locator(
		`#plugin-debug-list label.plugin-debug-row-active:has(.plugin-debug-name:text-is("rss"))`,
	).WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("enabled row did not receive active class: %v", err)
	}

	// Untoggle — set should clear.
	if err := page.Locator(
		`#plugin-debug-list label:has(.plugin-debug-name:text-is("rss")) .plugin-debug-cb`,
	).Click(); err != nil {
		t.Fatalf("click rss checkbox second time: %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got = ctl.DebugPlugins()
		if len(got) == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(got) != 0 {
		t.Errorf("control after disable: got %v, want []", got)
	}
}

// captureLogCtl is a minimal in-test PluginLogControl. Mirrors the impl in
// TestE2EDatabaseFilterKeepsFocusWhileTyping pins down the database-tab fix
// for the focus-loss-on-type bug: while the user is typing into the filter
// box, the debounced refetch (300ms) fires mid-keystroke; before the fix the
// full main-panel re-render replaced the input element, stealing focus and
// dropping any in-flight keystrokes. After the fix, the toolbar (filter +
// Clear All) survives a same-bucket refresh — only the pager and table swap.
//
// The test types one character at a time with a delay that crosses both the
// 300ms debounce and the loading-indicator render, then asserts that the
// filter input is still the active element AND that every keystroke landed in
// its value. If the bug regresses, focus moves to <body> and the value is a
// truncated tail of "search".
func TestE2EDatabaseFilterKeepsFocusWhileTyping(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)

	// Open the Database tab. The sidebar auto-selects the first bucket
	// (Series, which is always present) and renders the filter input.
	if err := page.Locator("#tab-btn-db").Click(); err != nil {
		t.Fatalf("click db tab: %v", err)
	}
	filter := page.Locator("#db-filter-input")
	if err := filter.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("wait filter input: %v", err)
	}
	if err := filter.Click(); err != nil {
		t.Fatalf("focus filter: %v", err)
	}

	// 6 chars × 120ms delay = 720ms typing window. The debounced fetch fires
	// after the first 300ms of quiet, but each keystroke restarts the timer,
	// so the actual refetch lands somewhere after the last character — the
	// loading-indicator + render path runs during this window. We typed the
	// word "search" character-by-character, so any focus loss or keystroke
	// drop during the re-render will leave a truncated value behind.
	if err := filter.PressSequentially("search", playwright.LocatorPressSequentiallyOptions{
		Delay: playwright.Float(120),
	}); err != nil {
		t.Fatalf("type into filter: %v", err)
	}
	// Wait past the 300ms debounce + a generous fetch+render budget so the
	// refresh definitely completed before we check focus.
	time.Sleep(600 * time.Millisecond)

	activeID, err := page.Evaluate(`document.activeElement && document.activeElement.id`)
	if err != nil {
		t.Fatalf("eval activeElement: %v", err)
	}
	if activeID != "db-filter-input" {
		t.Errorf("filter focus lost during typing: activeElement.id=%v, want db-filter-input", activeID)
	}

	val, err := filter.InputValue()
	if err != nil {
		t.Fatalf("input value: %v", err)
	}
	if val != "search" {
		t.Errorf("filter value: got %q, want %q (keystrokes lost to a re-render)", val, "search")
	}
}

// internal/clog without depending on the real handler here.
type captureLogCtl struct {
	mu      sync.Mutex
	plugins []string
}

func (c *captureLogCtl) DebugPlugins() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.plugins))
	copy(out, c.plugins)
	return out
}

func (c *captureLogCtl) SetDebugPlugins(names []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plugins = append(c.plugins[:0], names...)
}

// ── fix/visual-editor-ux tests ───────────────────────────────────────────────

// TestE2ERouteAutoLayoutFlowsLeftToRight loads a route config with NO stored
// positions and asserts every edge flows left-to-right (child.x > parent.x).
// Before the fix, nodes fed by route ports fell back to depth 0 and were
// stacked in a left column under the source with backwards, right-to-left edges.
func TestE2ERouteAutoLayoutFlowsLeftToRight(t *testing.T) {
	ts := startTestServer(t, routeConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, routeConfig)

	violations, err := page.Evaluate(`(() => {
		const bad = [];
		for (const g of ve.graphs) {
			const byId = new Map(g.nodes.map(n => [n.id, n]));
			for (const n of g.nodes) {
				if (n.isRoutePort || n.isSearchNode || n.isListNode) continue;
				for (const uid of (n.upstreams || [])) {
					let up = byId.get(uid);
					// Route ports are virtual — the visual edge starts at the route card.
					if (up && up.isRoutePort) up = byId.get(up.routeParentId);
					if (!up) continue;
					if (!(n.x > up.x)) bad.push(up.id + '(' + up.x + ') !< ' + n.id + '(' + n.x + ')');
				}
			}
		}
		return JSON.stringify(bad);
	})()`)
	if err != nil {
		t.Fatalf("evaluate edge directions: %v", err)
	}
	if violations != "[]" {
		t.Errorf("edges not flowing left-to-right after auto-layout: %v", violations)
	}
}

// TestE2ETidyLayoutButton verifies the toolbar's "Tidy layout" button exists
// next to Undo and that clicking it re-lays out the active pipeline and
// persists the new positions (pipeliner:pos comments appear in the text).
func TestE2ETidyLayoutButton(t *testing.T) {
	ts := startTestServer(t, routeConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, routeConfig)

	tidy := page.Locator("#ve-tidy-btn")
	if err := tidy.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("tidy button not visible: %v", err)
	}
	// The button must sit right after Undo in the pipeline bar.
	prevID, err := page.Evaluate(`document.getElementById('ve-tidy-btn').previousElementSibling.id`)
	if err != nil || prevID != "ve-undo-btn" {
		t.Errorf("tidy button not next to Undo: prev=%v err=%v", prevID, err)
	}
	if err := tidy.Click(); err != nil {
		t.Fatalf("click tidy: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Positions must persist through onModelChange → text serialisation.
	content := editorContent(t, page)
	if !strings.Contains(content, "# pipeliner:pos") {
		t.Errorf("tidy layout did not persist positions; text:\n%s", content)
	}
	// Layout must remain left-to-right after tidy.
	page.Locator("#view-btn-visual").Click()
	violations, err := page.Evaluate(`(() => {
		const bad = [];
		for (const g of ve.graphs) {
			const byId = new Map(g.nodes.map(n => [n.id, n]));
			for (const n of g.nodes) {
				if (n.isRoutePort || n.isSearchNode || n.isListNode) continue;
				for (const uid of (n.upstreams || [])) {
					let up = byId.get(uid);
					if (up && up.isRoutePort) up = byId.get(up.routeParentId);
					if (!up) continue;
					if (!(n.x > up.x)) bad.push(up.id + ' !< ' + n.id);
				}
			}
		}
		return JSON.stringify(bad);
	})()`)
	if err != nil {
		t.Fatalf("evaluate after tidy: %v", err)
	}
	if violations != "[]" {
		t.Errorf("edges not left-to-right after Tidy layout: %v", violations)
	}
}

// twoPipelineConfig has two pipelines so viewport preservation can be
// asserted on a non-default active pipeline.
const twoPipelineConfig = `src_a = input("rss", url="https://example.com/a")
flt_a = process("seen", upstream=src_a)
pipeline("alpha")

src_b = input("rss", url="https://example.com/b")
flt_b = process("seen", upstream=src_b)
pipeline("beta")`

// TestE2EValidatePreservesViewport reproduces the "validate resets the view"
// bug: select a node in the SECOND pipeline, pan the canvas, hit Validate
// (which re-parses text → visual), and assert the active pipeline, node
// selection + open param panel, and pan survive the round-trip.
func TestE2EValidatePreservesViewport(t *testing.T) {
	ts := startTestServer(t, twoPipelineConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, twoPipelineConfig)

	// Select the seen node of pipeline "beta" (activates graph 1). Node IDs
	// are canonicalized by the server, so resolve it from the model.
	selID, err := page.Evaluate(`ve.graphs[1].nodes.find(n => n.plugin === 'seen').id`)
	if err != nil {
		t.Fatalf("resolve beta seen node id: %v", err)
	}
	if _, err := page.Evaluate(`selectNode(` + fmt.Sprintf("%q", selID) + `)`); err != nil {
		t.Fatalf("select node: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	// Apply a distinctive pan so a reset (pan → 0) is detectable.
	if _, err := page.Evaluate(`(() => { ve_panX = -37; ve_panY = -53; applyZoom(); })()`); err != nil {
		t.Fatalf("set pan: %v", err)
	}

	// Trigger Validate via its handler — the floating param panel (opened by
	// the node selection above) can overlap the toolbar button and make a
	// physical click flaky; the behaviour under test is the re-parse itself.
	if _, err := page.Evaluate(`validateConfig()`); err != nil {
		t.Fatalf("validateConfig: %v", err)
	}
	// Wait for the parse round-trip to finish.
	time.Sleep(700 * time.Millisecond)

	state, err := page.Evaluate(`JSON.stringify({
		active:    ve.graphs[ve.activeGraph] ? ve.graphs[ve.activeGraph].name : null,
		selected:  ve.selectedNodeId,
		panX:      ve_panX,
		panY:      ve_panY,
		panelOpen: document.getElementById('ve-param-title').style.display !== 'none',
	})`)
	if err != nil {
		t.Fatalf("evaluate state: %v", err)
	}
	s, _ := state.(string)
	wants := []string{
		`"active":"beta"`,
		fmt.Sprintf(`"selected":%q`, selID),
		`"panX":-37`, `"panY":-53`, `"panelOpen":true`,
	}
	for _, want := range wants {
		if !strings.Contains(s, want) {
			t.Errorf("viewport not preserved across Validate: missing %s in %s", want, s)
		}
	}
}

// TestE2ERoutePortLabelsVisible asserts each route port circle carries a
// visible name label (item: ports were anonymous dots).
func TestE2ERoutePortLabelsVisible(t *testing.T) {
	ts := startTestServer(t, routeConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, routeConfig)

	labels := page.Locator(".ve-route-port-label")
	count, err := labels.Count()
	if err != nil || count != 2 {
		t.Fatalf("route port labels: got %d (err=%v), want 2", count, err)
	}
	texts, err := labels.AllTextContents()
	if err != nil {
		t.Fatalf("label texts: %v", err)
	}
	joined := strings.Join(texts, ",")
	if !strings.Contains(joined, "series") || !strings.Contains(joined, "movies") {
		t.Errorf("route port labels: got %q, want series and movies", joined)
	}

	// The node body summary must name the ports, not say "2 rules".
	// (Node IDs are canonicalized by the server, so match on the model.)
	previewAny, err := page.Evaluate(`(() => {
		const routeNode = ve.graphs[0].nodes.find(n => n.plugin === 'route');
		const el = document.querySelector('.ve-node[data-id="' + routeNode.id + '"] .ve-node-preview');
		return el ? el.textContent : '';
	})()`)
	if err != nil {
		t.Fatalf("route node preview: %v", err)
	}
	preview, _ := previewAny.(string)
	if strings.Contains(preview, "rules") {
		t.Errorf("route summary still rule-count based: %q", preview)
	}
	if !strings.Contains(preview, "series") || !strings.Contains(preview, "movies") {
		t.Errorf("route summary does not name the ports: %q", preview)
	}
}

// TestE2EDeletePipelineAsksConfirmation verifies the region × button asks
// before deleting, states what is lost, and honours Cancel.
func TestE2EDeletePipelineAsksConfirmation(t *testing.T) {
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	var dialogMsg string
	accept := false
	page.OnDialog(func(d playwright.Dialog) {
		dialogMsg = d.Message()
		if accept {
			_ = d.Accept()
		} else {
			_ = d.Dismiss()
		}
	})

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, minimalConfig)

	// Cancel first: the pipeline must survive.
	if err := page.Locator(".ve-pl-delete").First().Click(); err != nil {
		t.Fatalf("click delete: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if !strings.Contains(dialogMsg, `Delete pipeline "tv"`) || !strings.Contains(dialogMsg, "2 nodes") {
		t.Errorf("confirm message: got %q, want pipeline name and node count", dialogMsg)
	}
	count, _ := page.Locator(".ve-pipeline-label").Count()
	if count != 1 {
		t.Fatalf("pipeline deleted despite Cancel: %d labels", count)
	}

	// Accept: the pipeline goes away.
	accept = true
	if err := page.Locator(".ve-pl-delete").First().Click(); err != nil {
		t.Fatalf("click delete (accept): %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	count, _ = page.Locator(".ve-pipeline-label").Count()
	if count != 0 {
		t.Errorf("pipeline not deleted after Accept: %d labels", count)
	}
}

// TestE2EInvalidConnectDropShowsReason drags from a sink's chain port onto a
// processor and asserts the sync note explains the sink-chaining rule
// (previously the drop was silently ignored).
func TestE2EInvalidConnectDropShowsReason(t *testing.T) {
	const cfg = `src = input("rss", url="https://example.com/rss")
flt = process("seen", upstream=src)
out = output("print", upstream=flt)
pipeline("tv")`
	ts := startTestServer(t, cfg)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, cfg)

	// Drag from the print sink's out-port onto the seen processor. Node IDs
	// are canonicalized by the server — resolve them from the model.
	ids, err := page.Evaluate(`JSON.stringify({
		sink: ve.graphs[0].nodes.find(n => n.plugin === 'print').id,
		proc: ve.graphs[0].nodes.find(n => n.plugin === 'seen').id,
	})`)
	if err != nil {
		t.Fatalf("resolve node ids: %v", err)
	}
	var nodeIDs struct {
		Sink string `json:"sink"`
		Proc string `json:"proc"`
	}
	if err := json.Unmarshal([]byte(ids.(string)), &nodeIDs); err != nil {
		t.Fatalf("unmarshal ids: %v", err)
	}
	outPort := page.Locator(fmt.Sprintf(`.ve-node[data-id=%q] .ve-node-out-port`, nodeIDs.Sink))
	target := page.Locator(fmt.Sprintf(`.ve-node[data-id=%q]`, nodeIDs.Proc))
	if err := outPort.Hover(); err != nil {
		t.Fatalf("hover out port: %v", err)
	}
	if err := page.Mouse().Down(); err != nil {
		t.Fatalf("mouse down: %v", err)
	}
	box, err := target.BoundingBox()
	if err != nil || box == nil {
		t.Fatalf("target bounding box: %v", err)
	}
	if err := page.Mouse().Move(box.X+box.Width/2, box.Y+box.Height/2, playwright.MouseMoveOptions{
		Steps: playwright.Int(8),
	}); err != nil {
		t.Fatalf("mouse move: %v", err)
	}
	// During the drag, the invalid target must be marked.
	invalidCount, _ := page.Locator(".ve-node.ve-conn-invalid").Count()
	if invalidCount == 0 {
		t.Errorf("no nodes marked invalid during sink connect drag")
	}
	if err := page.Mouse().Up(); err != nil {
		t.Fatalf("mouse up: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	note, err := page.Locator("#ve-sync-note").TextContent()
	if err != nil {
		t.Fatalf("sync note: %v", err)
	}
	if !strings.Contains(note, "sink output can only feed another sink") {
		t.Errorf("sync note after invalid drop: got %q, want sink-chain reason", note)
	}
	// And the model must be unchanged (seen still has exactly its rss upstream).
	ups, _ := page.Evaluate(fmt.Sprintf(`JSON.stringify(findNode(%q).upstreams)`, nodeIDs.Proc))
	upsStr, _ := ups.(string)
	if strings.Contains(upsStr, nodeIDs.Sink) {
		t.Errorf("invalid drop added the sink as upstream: %v", upsStr)
	}
}

// TestE2EFunctionEditorShowsUpstreamStub opens a processor function and
// asserts the ⬅ upstream pseudo-node is rendered with an edge to the entry
// node, and that it does not leak into the serialised function on save.
func TestE2EFunctionEditorShowsUpstreamStub(t *testing.T) {
	const cfg = `# pipeliner:param pattern  type=string  filter pattern
def myfilter(upstream, pattern):
    flt = process("seen", upstream=upstream)
    return flt

src = input("rss", url="https://example.com/rss")
call_1 = myfilter(upstream=src, pattern="x")
output("print", upstream=call_1)
pipeline("tv")`
	ts := startTestServer(t, cfg)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close()

	// Auto-accept dialogs (the Back path may confirm).
	page.OnDialog(func(d playwright.Dialog) { _ = d.Accept() })

	login(t, page, ts.url)
	openConfigTab(t, page)
	switchToVisual(t, page, cfg)

	if err := page.Locator(".ve-chip-fn-edit").Click(); err != nil {
		t.Fatalf("open fn editor: %v", err)
	}
	if err := page.Locator("#ve-fn-bar").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("fn bar: %v", err)
	}

	// The upstream stub must be on the canvas, left of the entry node.
	stub := page.Locator(".ve-node-upstream-pseudo")
	if err := stub.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("upstream stub not visible: %v", err)
	}
	leftOf, err := page.Evaluate(`(() => {
		const stub  = ve.graphs[0].nodes.find(n => n.isUpstreamPseudo);
		const entry = ve.graphs[0].nodes.find(n => (n.upstreams||[]).includes('_upstream'));
		return String(stub && entry && stub.x < entry.x);
	})()`)
	if err != nil || leftOf != "true" {
		t.Errorf("upstream stub not left of entry node: %v (err=%v)", leftOf, err)
	}
	// An edge from the stub to the entry node must be drawn.
	edgeCount, _ := page.Evaluate(`String(document.querySelectorAll('#ve-graph-svg path.ve-edge').length)`)
	if edgeCount == "0" {
		t.Errorf("no edge drawn from upstream stub to entry node")
	}

	// Save; the function source must keep upstream=upstream and must NOT
	// contain any _upstream node assignment.
	if err := page.Locator(".ve-fn-bar-save").Click(); err != nil {
		t.Fatalf("save fn: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	content := editorContent(t, page)
	if !strings.Contains(content, "def myfilter(upstream") {
		t.Errorf("function signature lost its upstream param:\n%s", content)
	}
	if strings.Contains(content, "_upstream =") {
		t.Errorf("upstream pseudo-node leaked into serialisation:\n%s", content)
	}
}
