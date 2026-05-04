// Package web provides a web-based UI and API for monitoring and controlling pipeliner.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

//go:embed ui/index.html
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
	tasksMu sync.RWMutex
	tasks   []TaskInfo
	daemon  DaemonControl
	history *History
	bcast   *Broadcaster
	reload  func() error // nil if reload is not configured

	runMu   sync.Mutex
	running map[string]int // task name → active run count
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
func (s *Server) TaskDone(name string) {
	s.runMu.Lock()
	s.running[name]--
	if s.running[name] <= 0 {
		delete(s.running, name)
	}
	s.runMu.Unlock()
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
func New(tasks []TaskInfo, d DaemonControl, h *History, b *Broadcaster) *Server {
	return &Server{tasks: tasks, daemon: d, history: h, bcast: b}
}

// SetReload configures the function called when the user requests a config reload.
func (s *Server) SetReload(fn func() error) { s.reload = fn }

// SetTasks atomically replaces the task list shown in the UI.
func (s *Server) SetTasks(tasks []TaskInfo) {
	s.tasksMu.Lock()
	s.tasks = tasks
	s.tasksMu.Unlock()
}

// Start begins listening on addr and blocks until ctx is cancelled.
func (s *Server) Start(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.serveUI)
	mux.HandleFunc("GET /api/status", s.apiStatus)
	mux.HandleFunc("GET /api/history", s.apiHistory)
	mux.HandleFunc("POST /api/tasks/{name}/run", s.apiTrigger)
	mux.HandleFunc("POST /api/tasks/run", s.apiRunAll)
	mux.HandleFunc("POST /api/reload", s.apiReload)
	mux.HandleFunc("GET /api/logs", s.apiLogs)

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
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

	snap, ch := s.bcast.Subscribe()
	defer s.bcast.Unsubscribe(ch)

	// Replay buffered lines from the last run before going live.
	for _, line := range snap {
		escaped := strings.ReplaceAll(line, "\n", "\ndata: ")
		fmt.Fprintf(w, "data: %s\n\n", escaped)
	}
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case line := <-ch:
			escaped := strings.ReplaceAll(line, "\n", "\ndata: ")
			fmt.Fprintf(w, "data: %s\n\n", escaped)
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
