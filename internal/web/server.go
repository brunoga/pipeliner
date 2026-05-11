// Package web provides a web-based UI and API for monitoring and controlling pipeliner.
package web

import (
	"context"
	"crypto/tls"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/brunoga/pipeliner/docs"
	"github.com/brunoga/pipeliner/internal/config"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

//go:embed ui
var uiFS embed.FS

// DaemonControl is the scheduler interface the Server uses.
type DaemonControl interface {
	NextRun(name string) time.Time
	Trigger(name string)
}

// TaskInfo describes one task shown in the UI.
type TaskInfo struct {
	Name     string
	Schedule string // empty for unscheduled (manual-only) tasks
}

// Server is the HTTP status interface for the daemon.
type Server struct {
	tasksMu  sync.RWMutex
	tasks    []TaskInfo
	daemon   DaemonControl
	history  *History
	bcast    *Broadcaster
	reload   func() error // nil if reload is not configured
	version  string
	creds    credentials
	sessions *sessionStore
	secure   bool // true when serving over TLS; controls the Secure cookie flag

	runMu         sync.Mutex
	running       map[string]int // task name → active run count
	pendingReload bool           // reload queued until all tasks are idle

	configPath      string                 // path to config file on disk
	validateConfig  func([]byte) []string  // returns validation error strings; nil if not set
	db              *store.SQLiteStore     // nil if not set

	traktAuthMu sync.Mutex
	traktAuth   *traktAuthSession
}

// TaskStarted records that a task has begun executing.
// When the first task of a new batch starts (idle → running), the log
// buffer is cleared so clients see only the current run's output.
func (s *Server) TaskStarted(name string) {
	s.runMu.Lock()
	if s.running == nil {
		s.running = make(map[string]int)
	}
	wasIdle := len(s.running) == 0
	s.running[name]++
	s.runMu.Unlock()
	if wasIdle && s.bcast != nil {
		s.bcast.Reset()
	}
}

// TaskDone records that a task execution has finished.
// If a config save is pending and all tasks are now idle, the reload fires.
func (s *Server) TaskDone(name string) {
	s.runMu.Lock()
	s.running[name]--
	if s.running[name] <= 0 {
		delete(s.running, name)
	}
	shouldReload := len(s.running) == 0 && s.pendingReload
	if shouldReload {
		s.pendingReload = false
	}
	s.runMu.Unlock()
	if shouldReload && s.reload != nil {
		_ = s.reload()
	}
}

func (s *Server) anyRunning() bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return len(s.running) > 0
}

func (s *Server) isRunning(name string) bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.running[name] > 0
}

// New creates a Server. Call Start to begin serving.
// username and password are required; all routes are protected by session auth.
func New(tasks []TaskInfo, d DaemonControl, h *History, b *Broadcaster, version, username, password string) *Server {
	return &Server{
		tasks:    tasks,
		daemon:   d,
		history:  h,
		bcast:    b,
		version:  version,
		creds:    newCredentials(username, password),
		sessions: newSessionStore(),
	}
}

// SetReload configures the function called when the user requests a config reload.
func (s *Server) SetReload(fn func() error) { s.reload = fn }

// SetConfigPath sets the path to the config file for the editor endpoints.
func (s *Server) SetConfigPath(path string) { s.configPath = path }

// SetConfigValidator sets the function used to validate raw Starlark config before saving.
// It returns a slice of human-readable error strings (empty = valid).
func (s *Server) SetConfigValidator(fn func([]byte) []string) { s.validateConfig = fn }

// SetStore wires the SQLite store so the database API endpoints can read and
// modify tracker data and caches.
func (s *Server) SetStore(db *store.SQLiteStore) { s.db = db }

// SetTasks atomically replaces the task list shown in the UI.
func (s *Server) SetTasks(tasks []TaskInfo) {
	s.tasksMu.Lock()
	s.tasks = tasks
	s.tasksMu.Unlock()
}

// Start begins listening on addr and blocks until ctx is cancelled.
// If tlsCfg is non-nil the server speaks HTTPS; otherwise plain HTTP is used
// (suitable for running behind a reverse proxy that terminates TLS).
func (s *Server) Start(ctx context.Context, addr string, tlsCfg *tls.Config) error {
	s.secure = tlsCfg != nil

	// Unauthenticated routes.
	open := http.NewServeMux()
	open.HandleFunc("GET /login", s.handleLoginGet)
	open.HandleFunc("POST /login", s.handleLoginPost)
	open.HandleFunc("POST /logout", s.handleLogout)

	// Authenticated routes wrapped in session middleware.
	protected := http.NewServeMux()
	protected.HandleFunc("GET /{$}", s.serveUI) // exact root only; {$} prevents subtree match
	protected.HandleFunc("GET /guide", s.serveGuide)
	protected.Handle("/", s.staticHandler()) // catch-all for CSS/JS assets (style.css, *.js)
	protected.HandleFunc("GET /api/status", s.apiStatus)
	protected.HandleFunc("GET /api/history", s.apiHistory)
	protected.HandleFunc("POST /api/tasks/{name}/run", s.apiTrigger)
	protected.HandleFunc("POST /api/tasks/run", s.apiRunAll)
	protected.HandleFunc("POST /api/reload", s.apiReload)
	protected.HandleFunc("GET /api/logs", s.apiLogs)
	protected.HandleFunc("GET /api/config", s.apiGetConfig)
	protected.HandleFunc("POST /api/config", s.apiSaveConfig)
	protected.HandleFunc("GET /api/plugins", s.apiPlugins)
	protected.HandleFunc("POST /api/config/parse", s.apiConfigParse)
	protected.HandleFunc("GET /api/db/buckets", s.apiDBBuckets)
	protected.HandleFunc("GET /api/db/buckets/{name}", s.apiDBGetBucket)
	protected.HandleFunc("DELETE /api/db/buckets/{name}", s.apiDBClearBucket)
	protected.HandleFunc("DELETE /api/db/entries/{name}", s.apiDBDeleteEntry)
	protected.HandleFunc("POST /api/trakt/auth/start", s.apiTraktAuthStart)
	protected.HandleFunc("GET /api/trakt/auth/poll", s.apiTraktAuthPoll)

	// Top-level mux: open routes take priority; everything else goes through auth.
	top := http.NewServeMux()
	top.Handle("/login", open)
	top.Handle("/logout", open)
	top.Handle("/", s.requireSession(protected))

	srv := &http.Server{
		Addr:              addr,
		Handler:           top,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         tlsCfg,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	if tlsCfg != nil {
		// TLSConfig already has certificates loaded; pass empty strings to use them.
		if err := srv.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
			return err
		}
	} else {
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			return err
		}
	}
	return nil
}

// --- handlers ---

func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, _ := uiFS.ReadFile("ui/index.html")
	html := strings.ReplaceAll(string(data), "__VERSION__", s.version)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprint(w, html)

}

// staticHandler returns an http.Handler that serves CSS/JS files from the
// embedded ui/ directory. Exposed so tests can wire it without starting a
// full authenticated server.
func (s *Server) staticHandler() http.Handler {
	subFS, _ := fs.Sub(uiFS, "ui")
	return http.FileServer(http.FS(subFS))
}

func (s *Server) serveGuide(w http.ResponseWriter, r *http.Request) {
	data, _ := docs.FS.ReadFile("user-guide.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) apiStatus(w http.ResponseWriter, _ *http.Request) {
	type taskJSON struct {
		Name    string `json:"name"`
		Schedule string `json:"schedule"`
		NextRun string `json:"nextRun,omitempty"`
		Running bool   `json:"running,omitempty"`
	}
	type resp struct {
		Tasks []taskJSON `json:"tasks"`
	}
	s.tasksMu.RLock()
	snap := s.tasks
	s.tasksMu.RUnlock()
	tasks := make([]taskJSON, len(snap))
	for i, t := range snap {
		tj := taskJSON{Name: t.Name, Schedule: t.Schedule, Running: s.isRunning(t.Name)}
		if next := s.daemon.NextRun(t.Name); !next.IsZero() {
			tj.NextRun = next.UTC().Format(time.RFC3339)
		}
		tasks[i] = tj
	}
	writeJSON(w, resp{Tasks: tasks})
}

func (s *Server) apiHistory(w http.ResponseWriter, _ *http.Request) {
	type runJSON struct {
		At       string `json:"at"`
		Accepted int    `json:"accepted"`
		Rejected int    `json:"rejected"`
		Failed   int    `json:"failed"`
		Total    int    `json:"total"`
		Duration string `json:"duration"`
		Err      string `json:"err,omitempty"`
	}
	all := s.history.All()
	out := make(map[string][]runJSON, len(all))
	for task, runs := range all {
		rj := make([]runJSON, len(runs))
		for i, r := range runs {
			rj[i] = runJSON{
				At:       r.At.UTC().Format(time.RFC3339),
				Accepted: r.Accepted,
				Rejected: r.Rejected,
				Failed:   r.Failed,
				Total:    r.Total,
				Duration: r.Duration.Round(time.Millisecond).String(),
				Err:      r.Err,
			}
		}
		out[task] = rj
	}
	writeJSON(w, out)
}

func (s *Server) apiRunAll(w http.ResponseWriter, _ *http.Request) {
	s.tasksMu.RLock()
	snap := s.tasks
	s.tasksMu.RUnlock()
	for _, t := range snap {
		s.daemon.Trigger(t.Name)
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) apiReload(w http.ResponseWriter, _ *http.Request) {
	if s.reload == nil {
		http.Error(w, "reload not configured", http.StatusNotImplemented)
		return
	}
	if s.anyRunning() {
		http.Error(w, "cannot reload while tasks are running", http.StatusConflict)
		return
	}
	if err := s.reload(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiTrigger(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.tasksMu.RLock()
	snap := s.tasks
	s.tasksMu.RUnlock()
	found := false
	for _, t := range snap {
		if t.Name == name {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	s.daemon.Trigger(name)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) apiLogs(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Parse Last-Event-ID so reconnecting clients only receive lines they
	// have not seen yet, preventing the buffer from being replayed on reconnect.
	var afterSeq int64
	if s := r.Header.Get("Last-Event-ID"); s != "" {
		fmt.Sscanf(s, "%d", &afterSeq) //nolint:errcheck
	}

	snap, ch := s.bcast.Subscribe(afterSeq)
	defer s.bcast.Unsubscribe(ch)

	for _, ll := range snap {
		escaped := strings.ReplaceAll(ll.text, "\n", "\ndata: ")
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ll.seq, escaped)
	}
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ll := <-ch:
			escaped := strings.ReplaceAll(ll.text, "\n", "\ndata: ")
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ll.seq, escaped)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) apiGetConfig(w http.ResponseWriter, _ *http.Request) {
	if s.configPath == "" {
		http.Error(w, "config path not set", http.StatusNotImplemented)
		return
	}
	data, err := os.ReadFile(s.configPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"content": string(data)})
}

func (s *Server) apiSaveConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
		DryRun  bool   `json:"dry_run"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	data := []byte(req.Content)

	// Always validate first (works even without a config path on disk).
	if s.validateConfig != nil {
		if errs := s.validateConfig(data); len(errs) > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": errs})
			return
		}
	}

	// Dry-run: validation passed, don't write.
	// configPath is only required for actual saves, not for validation.
	if req.DryRun {
		writeJSON(w, map[string]string{"status": "valid"})
		return
	}

	if s.configPath == "" {
		http.Error(w, "config path not set", http.StatusNotImplemented)
		return
	}
	if err := os.WriteFile(s.configPath, data, 0600); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Reload immediately if idle, otherwise queue for when tasks finish.
	s.runMu.Lock()
	idle := len(s.running) == 0
	if !idle {
		s.pendingReload = true
	}
	s.runMu.Unlock()

	if idle && s.reload != nil {
		if err := s.reload(); err != nil {
			writeJSON(w, map[string]string{"status": "saved", "warning": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "reloaded"})
		return
	}
	writeJSON(w, map[string]string{"status": "pending"})
}


// apiPlugins returns all registered plugins with their metadata and optional
// field schema, for use by the visual pipeline editor's plugin palette.
func (s *Server) apiPlugins(w http.ResponseWriter, _ *http.Request) {
	type fieldResp struct {
		Key      string   `json:"key"`
		Type     string   `json:"type"`
		Required bool     `json:"required"`
		Default  any      `json:"default,omitempty"`
		Enum     []string `json:"enum,omitempty"`
		Hint     string   `json:"hint,omitempty"`
	}
	type pluginResp struct {
		Name        string      `json:"name"`
		Role        string      `json:"role"`
		Description string      `json:"description"`
		Produces    []string    `json:"produces"` // entry field names this plugin writes
		Requires    []string    `json:"requires"` // entry field names this plugin reads
		Schema      []fieldResp `json:"schema"`   // empty slice, never null
	}

	descs := plugin.All()
	out := make([]pluginResp, 0, len(descs))
	for _, d := range descs {
		fields := make([]fieldResp, 0, len(d.Schema))
		for _, f := range d.Schema {
			fields = append(fields, fieldResp{
				Key:      f.Key,
				Type:     string(f.Type),
				Required: f.Required,
				Default:  f.Default,
				Enum:     f.Enum,
				Hint:     f.Hint,
			})
		}
		produces := d.Produces
		if produces == nil {
			produces = []string{}
		}
		requires := d.Requires
		if requires == nil {
			requires = []string{}
		}
		out = append(out, pluginResp{
			Name:        d.PluginName,
			Role:        string(d.EffectiveRole()),
			Description: d.Description,
			Produces:    produces,
			Requires:    requires,
			Schema:      fields,
		})
	}
	writeJSON(w, out)
}

// apiConfigParse executes a Starlark config string server-side and returns
// the resolved Config as JSON. Used by the visual editor's Text→Visual sync.
func (s *Server) apiConfigParse(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	c, err := config.ParseBytes([]byte(req.Content))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// DAG graphs.
	type nodeResp struct {
		ID         string         `json:"id"`
		PluginName string         `json:"plugin"`
		Config     map[string]any `json:"config"`
		Upstreams  []string       `json:"upstreams"`
	}
	type graphResp struct {
		Nodes    []nodeResp `json:"nodes"`
		Schedule string     `json:"schedule,omitempty"`
	}
	graphs := make(map[string]graphResp, len(c.Graphs))
	for name, g := range c.Graphs {
		nodes := make([]nodeResp, 0, g.Len())
		for _, n := range g.Nodes() {
			ups := make([]string, len(n.Upstreams))
			for i, u := range n.Upstreams {
				ups[i] = string(u)
			}
			cfg := n.Config
			if cfg == nil {
				cfg = map[string]any{}
			}
			nodes = append(nodes, nodeResp{
				ID:         string(n.ID),
				PluginName: n.PluginName,
				Config:     cfg,
				Upstreams:  ups,
			})
		}
		graphs[name] = graphResp{Nodes: nodes, Schedule: c.GraphSchedules[name]}
	}

	writeJSON(w, map[string]any{"graphs": graphs})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
