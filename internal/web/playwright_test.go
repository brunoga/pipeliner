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
	_ "github.com/brunoga/pipeliner/plugins/processor/filter/seen"
	_ "github.com/brunoga/pipeliner/plugins/processor/modify/pathfmt"
	_ "github.com/brunoga/pipeliner/plugins/source/rss"
	_ "github.com/brunoga/pipeliner/plugins/source/rss_search"
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
	srv.SetConfigValidator(func(data []byte) []string {
		c, err := config.ParseBytes(data)
		if err != nil {
			return []string{err.Error()}
		}
		errs := config.Validate(c)
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return msgs
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
	if err := page.Locator("#view-text").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("wait for text view: %v", err)
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

	// The text editor should contain the config (served by GET /api/config,
	// which reads from disk — here it's not set so editor may be empty;
	// the important thing is the editor is present and the tab works).
	if err := page.Locator("#config-editor").WaitFor(); err != nil {
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

	// Write DAG-syntax Starlark into the text editor.
	starlark := fmt.Sprintf("src = input(\"rss\", url=%q)\npipeline(\"my-pipeline\")", "https://example.com")
	if err := page.Locator("#config-editor").Fill(starlark); err != nil {
		t.Fatalf("fill editor: %v", err)
	}

	// Switching to visual auto-parses the text config.
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
