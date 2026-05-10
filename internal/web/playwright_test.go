//go:build e2e

// E2E browser tests using playwright-go. Build with -tags e2e.
// Requires Chromium: go run github.com/playwright-community/playwright-go/cmd/playwright install chromium
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
	"github.com/brunoga/pipeliner/internal/task"
	"github.com/brunoga/pipeliner/internal/web"

	// Register plugins needed by test configs.
	_ "github.com/brunoga/pipeliner/plugins/filter/seen"
	_ "github.com/brunoga/pipeliner/plugins/input/rss"
	_ "github.com/brunoga/pipeliner/plugins/modify/pathfmt"
)

// ── test server setup ────────────────────────────────────────────────────────

type testServer struct {
	url    string
	server *http.Server
	done   chan struct{}
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
		t.Fatalf("playwright.Run: %v", err)
	}
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		pw.Stop() //nolint:errcheck
		t.Fatalf("launch chromium: %v", err)
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
	if err := page.Fill("#username", "admin"); err != nil {
		t.Fatalf("fill username: %v", err)
	}
	if err := page.Fill("#password", "password"); err != nil {
		t.Fatalf("fill password: %v", err)
	}
	if err := page.Click(`button[type="submit"]`); err != nil {
		t.Fatalf("submit login: %v", err)
	}
	if err := page.WaitForURL(baseURL + "/"); err != nil {
		t.Fatalf("wait for redirect: %v", err)
	}
}

func openConfigTab(t *testing.T, page playwright.Page) {
	t.Helper()
	if err := page.Click("#tab-btn-config"); err != nil {
		t.Fatalf("click config tab: %v", err)
	}
	if _, err := page.WaitForSelector("#view-text", playwright.PageWaitForSelectorOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("wait for text view: %v", err)
	}
}

// ── tests ────────────────────────────────────────────────────────────────────

const minimalConfig = `task("tv", [plugin("rss", url="https://example.com/rss"), plugin("seen")])`

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
	if _, err := page.WaitForSelector(".task-name", playwright.PageWaitForSelectorOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("wait for .task-name: %v", err)
	}
	taskName, err := page.TextContent(".task-name")
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
	if _, err := page.WaitForSelector("#config-editor"); err != nil {
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

	// Click the Visual toggle.
	if err := page.Click("#view-btn-visual"); err != nil {
		t.Fatalf("click visual toggle: %v", err)
	}

	// The plugin palette should become visible.
	if _, err := page.WaitForSelector("#ve-palette-body", playwright.PageWaitForSelectorOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("plugin palette not visible after toggle: %v", err)
	}

	// Text view should be hidden.
	textVisible, err := page.IsVisible("#view-text")
	if err != nil {
		t.Fatalf("IsVisible: %v", err)
	}
	if textVisible {
		t.Error("text view should be hidden when visual mode is active")
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

	// Switch to visual mode.
	if err := page.Click("#view-btn-visual"); err != nil {
		t.Fatalf("click visual toggle: %v", err)
	}
	if _, err := page.WaitForSelector("#ve-palette-body .ve-chip", playwright.PageWaitForSelectorOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Fatalf("no palette chips loaded: %v", err)
	}

	// Add a new task first.
	if err := page.Click(".ve-add-task"); err != nil {
		t.Fatalf("click add task: %v", err)
	}

	// Click the first palette chip to append a plugin.
	if err := page.Click("#ve-palette-body .ve-chip"); err != nil {
		t.Fatalf("click palette chip: %v", err)
	}

	// A plugin card should appear in the canvas.
	if _, err := page.WaitForSelector(".ve-node", playwright.PageWaitForSelectorOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("no plugin card appeared in canvas: %v", err)
	}
}

func TestE2EVisualToTextSync(t *testing.T) {
	// Visual changes automatically sync back to the text editor — no manual
	// "Sync to Text" button needed.
	ts := startTestServer(t, minimalConfig)
	browser, stop := pwSetup(t)
	defer stop()

	page, _ := browser.NewPage()
	defer page.Close() //nolint:errcheck

	login(t, page, ts.url)
	openConfigTab(t, page)

	// Switch to visual mode — this auto-parses the current text config.
	page.Click("#view-btn-visual")                    //nolint:errcheck
	page.WaitForSelector("#ve-palette-body .ve-chip") //nolint:errcheck

	// Add a new task and a plugin — each change writes back to the text editor.
	page.Click(".ve-add-task")               //nolint:errcheck
	page.Click("#ve-palette-body .ve-chip")  //nolint:errcheck
	page.WaitForSelector(".ve-node")         //nolint:errcheck

	// Switch back to text view and verify the editor reflects the visual state.
	if err := page.Click("#view-btn-text"); err != nil {
		t.Fatalf("click text toggle: %v", err)
	}
	editorContent, err := page.InputValue("#config-editor")
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

	// Put Starlark into the text editor.
	starlark := fmt.Sprintf(`task("my-tv-task", [plugin("rss", url=%q)])`, "https://example.com")
	if err := page.Fill("#config-editor", starlark); err != nil {
		t.Fatalf("fill editor: %v", err)
	}

	// Switching to visual automatically parses the text config — no button needed.
	page.Click("#view-btn-visual")       //nolint:errcheck
	page.WaitForSelector("#ve-task-bar") //nolint:errcheck

	// The task tab should show the task name.
	if _, err := page.WaitForSelector(`.ve-task-tab:has-text("my-tv-task")`, playwright.PageWaitForSelectorOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("task tab 'my-tv-task' not found after Text→Visual sync: %v", err)
	}

	// An rss plugin card should appear.
	if _, err := page.WaitForSelector(`.ve-node-name:has-text("rss")`, playwright.PageWaitForSelectorOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("rss plugin card not found after sync: %v", err)
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

	// Put valid Starlark in the editor.
	page.Fill("#config-editor", `task("t", [plugin("seen")])`) //nolint:errcheck

	// Click Validate.
	if err := page.Click(`button:has-text("Validate")`); err != nil {
		t.Fatalf("click validate: %v", err)
	}

	// Status should show success.
	if _, err := page.WaitForSelector(".config-status.ok", playwright.PageWaitForSelectorOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		t.Errorf("expected ok status after validating correct config: %v", err)
	}
}

// Ensure this file is not empty when build tag excludes it.
var _ task.Task
