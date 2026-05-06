package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/brunoga/pipeliner/internal/clog"
	"github.com/brunoga/pipeliner/internal/config"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/scheduler"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/task"
	"github.com/brunoga/pipeliner/internal/web"

	// Register all built-in plugins via side-effect imports.
	_ "github.com/brunoga/pipeliner/plugins/filter/accept_all"
	_ "github.com/brunoga/pipeliner/plugins/filter/condition"
	_ "github.com/brunoga/pipeliner/plugins/filter/content"
	_ "github.com/brunoga/pipeliner/plugins/filter/exists"
	_ "github.com/brunoga/pipeliner/plugins/filter/list_match"
	_ "github.com/brunoga/pipeliner/plugins/filter/movies"
	_ "github.com/brunoga/pipeliner/plugins/filter/premiere"
	_ "github.com/brunoga/pipeliner/plugins/filter/quality"
	_ "github.com/brunoga/pipeliner/plugins/filter/regexp"
	_ "github.com/brunoga/pipeliner/plugins/filter/require"
	_ "github.com/brunoga/pipeliner/plugins/filter/seen"
	_ "github.com/brunoga/pipeliner/plugins/filter/series"
	_ "github.com/brunoga/pipeliner/plugins/filter/torrentalive"
	_ "github.com/brunoga/pipeliner/plugins/filter/trakt"
	_ "github.com/brunoga/pipeliner/plugins/filter/tvdb"
	_ "github.com/brunoga/pipeliner/plugins/filter/upgrade"
	_ "github.com/brunoga/pipeliner/plugins/from/jackett"
	_ "github.com/brunoga/pipeliner/plugins/from/rss"
	_ "github.com/brunoga/pipeliner/plugins/from/trakt"
	_ "github.com/brunoga/pipeliner/plugins/from/tvdb"
	_ "github.com/brunoga/pipeliner/plugins/input/discover"
	_ "github.com/brunoga/pipeliner/plugins/input/filesystem"
	_ "github.com/brunoga/pipeliner/plugins/input/html"
	_ "github.com/brunoga/pipeliner/plugins/input/rss"
	_ "github.com/brunoga/pipeliner/plugins/input/search/jackett"
	_ "github.com/brunoga/pipeliner/plugins/metainfo/magnet"
	_ "github.com/brunoga/pipeliner/plugins/metainfo/quality"
	_ "github.com/brunoga/pipeliner/plugins/metainfo/series"
	_ "github.com/brunoga/pipeliner/plugins/metainfo/tmdb"
	_ "github.com/brunoga/pipeliner/plugins/metainfo/torrent"
	_ "github.com/brunoga/pipeliner/plugins/metainfo/trakt"
	_ "github.com/brunoga/pipeliner/plugins/metainfo/tvdb"
	_ "github.com/brunoga/pipeliner/plugins/modify/pathfmt"
	_ "github.com/brunoga/pipeliner/plugins/modify/pathscrub"
	_ "github.com/brunoga/pipeliner/plugins/modify/set"
	_ "github.com/brunoga/pipeliner/plugins/notify/email"
	_ "github.com/brunoga/pipeliner/plugins/notify/pushover"
	_ "github.com/brunoga/pipeliner/plugins/notify/webhook"
	_ "github.com/brunoga/pipeliner/plugins/output/decompress"
	_ "github.com/brunoga/pipeliner/plugins/output/deluge"
	_ "github.com/brunoga/pipeliner/plugins/output/download"
	_ "github.com/brunoga/pipeliner/plugins/output/email"
	_ "github.com/brunoga/pipeliner/plugins/output/exec"
	_ "github.com/brunoga/pipeliner/plugins/output/list_add"
	_ "github.com/brunoga/pipeliner/plugins/output/notify"
	_ "github.com/brunoga/pipeliner/plugins/output/print"
	_ "github.com/brunoga/pipeliner/plugins/output/qbittorrent"
)

// version is overridden at build time via:
//
//	go build -ldflags "-X main.version=$(git describe --tags --dirty --always)"
//
// When installed with "go install" (no ldflags), resolveVersion() falls back
// to the module version embedded by the Go toolchain via debug/buildinfo.
var version = "dev"

func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return version
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 1
	}

	switch args[0] {
	case "run":
		return cmdRun(args[1:])
	case "daemon":
		return cmdDaemon(args[1:])
	case "check":
		return cmdCheck(args[1:])
	case "list-plugins":
		return cmdListPlugins(args[1:])
	case "version":
		fmt.Printf("Pipeliner %s\n", resolveVersion())
		return 0
	case "help", "--help", "-h":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", args[0])
		printUsage()
		return 1
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Pipeliner — media automation tool

Usage:
  pipeliner run     [--config path] [--log-level level] [--log-plugin name] [--dry-run] [task ...]
  pipeliner daemon  [--config path] [--log-level level] [--log-plugin name]
                    [--web :8080 --web-user USER --web-password PASS]
                    [--tls-self-signed | --tls-cert cert.pem --tls-key key.pem]

  pipeliner check        [--config path]   validate config
  pipeliner list-plugins                   list registered plugins
  pipeliner version                        print version

Web UI flags:
  --web-user         username for the web interface (required with --web)
  --web-password     password for the web interface (required with --web)

TLS flags (optional; plain HTTP is used when none are set, suitable for a
           reverse proxy that terminates TLS externally):
  --tls-self-signed  generate a self-signed certificate at startup
  --tls-cert         path to a TLS certificate file (requires --tls-key)
  --tls-key          path to a TLS private key file  (requires --tls-cert)`)
}

// --- run command ---

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath   := fs.String("config",     "config.yaml", "path to config file")
	logLevel  := fs.String("log-level",  "info",        "log level (debug, info, warn, error)")
	logPlugin := fs.String("log-plugin", "",            "only show log output from this plugin (task-level logs always shown)")
	dryRun    := fs.Bool("dry-run",      false,         "execute pipeline but skip output phase")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	logger := makeLogger(*logLevel, *logPlugin)
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		logger.Error("failed to load config", "err", err)
		return 1
	}

	if errs := config.Validate(cfg); len(errs) > 0 {
		for _, e := range errs {
			logger.Error("config validation error", "err", e)
		}
		return 1
	}

	db, err := store.OpenSQLite(dbPath(*cfgPath))
	if err != nil {
		logger.Error("failed to open store", "err", err)
		return 1
	}
	defer db.Close()

	tasks, err := config.BuildTasks(cfg, db, logger)
	if err != nil {
		logger.Error("failed to build tasks", "err", err)
		return 1
	}

	// Filter tasks by name if specified on command line.
	wanted := map[string]bool{}
	for _, name := range fs.Args() {
		wanted[name] = true
	}

	if len(wanted) > 0 {
		knownTasks := make(map[string]bool)
		for _, t := range tasks {
			knownTasks[t.Name()] = true
		}
		for name := range wanted {
			if !knownTasks[name] {
				logger.Error("unknown task specified", "task", name)
				return 1
			}
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	exitCode := 0
	for _, t := range tasks {
		if len(wanted) > 0 && !wanted[t.Name()] {
			continue
		}
		if *dryRun {
			t.SetDryRun(true)
		}
		result, err := t.Run(ctx)

		if err != nil {
			logger.Error("task failed", "task", t.Name(), "err", err)
			exitCode = 2
			continue
		}
		logger.Info("task done",
			"task", t.Name(),
			"accepted", result.Accepted,
			"rejected", result.Rejected,
			"failed", result.Failed,
			"duration", result.Duration,
		)
	}
	return exitCode
}

// --- daemon command ---

func cmdDaemon(args []string) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	cfgPath       := fs.String("config",          "config.yaml", "path to config file")
	logLevel      := fs.String("log-level",       "info",        "log level (debug, info, warn, error)")
	logPlugin     := fs.String("log-plugin",      "",            "only show log output from this plugin (task-level logs always shown)")
	webAddr       := fs.String("web",             "",            "web interface listen address (e.g. :8080); empty disables it")
	webUser       := fs.String("web-user",        "",            "username for the web interface (required with --web)")
	webPass       := fs.String("web-password",    "",            "password for the web interface (required with --web)")
	tlsSelfSigned := fs.Bool("tls-self-signed",   false,         "generate a self-signed TLS certificate at startup")
	tlsCert       := fs.String("tls-cert",        "",            "path to TLS certificate file (requires --tls-key)")
	tlsKey        := fs.String("tls-key",         "",            "path to TLS private key file (requires --tls-cert)")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	if *webAddr != "" {
		if *webUser == "" || *webPass == "" {
			fmt.Fprintln(os.Stderr, "error: --web-user and --web-password are required when --web is set")
			return 1
		}
		if (*tlsCert == "") != (*tlsKey == "") {
			fmt.Fprintln(os.Stderr, "error: --tls-cert and --tls-key must be provided together")
			return 1
		}
		if *tlsSelfSigned && (*tlsCert != "" || *tlsKey != "") {
			fmt.Fprintln(os.Stderr, "error: --tls-self-signed cannot be combined with --tls-cert/--tls-key")
			return 1
		}
	}

	opts := logHandlerOptions(*logLevel)
	var bcast *web.Broadcaster
	var logger *slog.Logger
	if *webAddr != "" {
		bcast = web.NewBroadcaster()
		// Write to both stderr and the broadcaster so startup errors (config not
		// found, invalid config, etc.) are always visible on the terminal even
		// before any web client connects.
		h := clog.Multi(
			clog.New(os.Stderr, opts),
			clog.NewColored(bcast, opts),
		)
		if *logPlugin != "" {
			logger = slog.New(clog.NewPluginFilter(h, *logPlugin))
		} else {
			logger = slog.New(h)
		}
	} else {
		h := clog.New(os.Stderr, opts)
		if *logPlugin != "" {
			logger = slog.New(clog.NewPluginFilter(h, *logPlugin))
		} else {
			logger = slog.New(h)
		}
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		logger.Error("failed to load config", "err", err)
		return 1
	}

	if errs := config.Validate(cfg); len(errs) > 0 {
		for _, e := range errs {
			logger.Error("config validation error", "err", e)
		}
		return 1
	}

	db, err := store.OpenSQLite(dbPath(*cfgPath))
	if err != nil {
		logger.Error("failed to open store", "err", err)
		return 1
	}
	defer db.Close()

	tasks, err := config.BuildTasks(cfg, db, logger)
	if err != nil {
		logger.Error("failed to build tasks", "err", err)
		return 1
	}

	// taskByName is replaced atomically on reload; protect all accesses with taskMu.
	var taskMu sync.RWMutex
	taskByName := make(map[string]*task.Task, len(tasks))
	for _, t := range tasks {
		taskByName[t.Name()] = t
	}

	d := &scheduler.Daemon{}
	if scheduled, ok := addSchedules(d, cfg.Schedules, taskByName, logger); !ok {
		return 1
	} else {
		for _, s := range scheduled {
			logger.Info("scheduled", "task", s.Name, "schedule", cfg.Schedules[s.Name])
		}
	}

	hist := web.NewHistory()

	// ws is captured by both runner and reload closures below; declared before both.
	var ws *web.Server

	runner := func(ctx context.Context, name string) {
		if ws != nil {
			ws.TaskStarted(name)
			defer ws.TaskDone(name)
		}
		taskMu.RLock()
		t, ok := taskByName[name]
		taskMu.RUnlock()
		if !ok {
			return
		}
		at := time.Now()
		result, runErr := t.Run(ctx)

		rec := web.RunRecord{Task: name, At: at}
		if runErr != nil {
			rec.Err = runErr.Error()
			logger.Error("task failed", "task", name, "err", runErr)
		} else {
			rec.Accepted = result.Accepted
			rec.Rejected = result.Rejected
			rec.Failed = result.Failed
			rec.Total = result.Total
			rec.Duration = result.Duration
			logger.Info("task done",
				"task", name,
				"accepted", result.Accepted,
				"rejected", result.Rejected,
				"failed", result.Failed,
				"duration", result.Duration,
			)
		}
		hist.Add(rec)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	reload := func() error {
		newCfg, err := config.Load(*cfgPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		if errs := config.Validate(newCfg); len(errs) > 0 {
			return errs[0]
		}
		newTasks, err := config.BuildTasks(newCfg, db, logger)
		if err != nil {
			return fmt.Errorf("build tasks: %w", err)
		}
		newMap := make(map[string]*task.Task, len(newTasks))
		for _, t := range newTasks {
			newMap[t.Name()] = t
		}
		scheduled, ok := addSchedules(nil, newCfg.Schedules, newMap, logger)
		if !ok {
			return fmt.Errorf("invalid schedules in new config")
		}

		taskMu.Lock()
		taskByName = newMap
		taskMu.Unlock()

		d.Reset(scheduled)
		logger.Info("config reloaded", "tasks", len(newTasks))

		if ws != nil {
			infos := make([]web.TaskInfo, len(newTasks))
			for i, t := range newTasks {
				infos[i] = web.TaskInfo{Name: t.Name(), Schedule: newCfg.Schedules[t.Name()]}
			}
			ws.SetTasks(infos)
		}
		return nil
	}

	if *webAddr != "" {
		tlsCfg, fp, err := buildTLSConfig(*tlsSelfSigned, *tlsCert, *tlsKey)
		if err != nil {
			logger.Error("failed to set up TLS", "err", err)
			return 1
		}

		scheme := "http"
		if tlsCfg != nil {
			scheme = "https"
			if fp != "" {
				logger.Info("using self-signed TLS certificate", "fingerprint", fp)
			}
		}

		taskInfos := make([]web.TaskInfo, len(tasks))
		for i, t := range tasks {
			taskInfos[i] = web.TaskInfo{Name: t.Name(), Schedule: cfg.Schedules[t.Name()]}
		}
		ws = web.New(taskInfos, d, hist, bcast, resolveVersion(), *webUser, *webPass)
		ws.SetReload(reload)
		ws.SetConfigPath(*cfgPath)
		ws.SetConfigValidator(func(data []byte) []string {
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
		go func() {
			if err := ws.Start(ctx, *webAddr, tlsCfg); err != nil {
				logger.Error("web server error", "err", err)
			}
		}()
		logger.Info("web interface enabled", "addr", scheme+"://"+*webAddr)
	}

	logger.Info("daemon started")
	d.Run(ctx, runner)
	logger.Info("daemon stopped")
	return 0
}

// addSchedules parses schedule expressions from cfg and registers them on d
// (if non-nil). Returns the slice of ScheduledTasks and ok=true on success.
func addSchedules(d *scheduler.Daemon, schedules map[string]string, tasks map[string]*task.Task, logger *slog.Logger) ([]scheduler.ScheduledTask, bool) {
	var out []scheduler.ScheduledTask
	for name, expr := range schedules {
		if _, ok := tasks[name]; !ok {
			logger.Error("schedule references unknown task", "task", name)
			return nil, false
		}
		sched, err := scheduler.ParseInterval(expr)
		runAtStart := err == nil
		if err != nil {
			sched, err = scheduler.ParseCron(expr)
		}
		if err != nil {
			logger.Error("invalid schedule", "task", name, "expr", expr, "err", err)
			return nil, false
		}
		out = append(out, scheduler.ScheduledTask{Name: name, Schedule: sched, RunAtStart: runAtStart})
		if d != nil {
			d.Add(name, sched)
			if runAtStart {
				d.TriggerAtStart(name)
			}
		}
	}
	return out, true
}

// --- check command ---

func cmdCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	cfgPath := fs.String("config", "config.yaml", "path to config file")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	errs := config.Validate(cfg)
	if len(errs) == 0 {
		fmt.Println("config OK")
		return 0
	}
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "error: %v\n", e)
	}
	return 1
}

// --- list-plugins command ---

func cmdListPlugins(_ []string) int {
	descs := plugin.All()
	if len(descs) == 0 {
		fmt.Println("no plugins registered")
		return 0
	}
	fmt.Printf("%-24s %-10s %s\n", "NAME", "PHASE", "DESCRIPTION")
	fmt.Println(strings.Repeat("-", 60))
	for _, d := range descs {
		fmt.Printf("%-24s %-10s %s\n", d.PluginName, d.PluginPhase, d.Description)
	}
	return 0
}

// --- helpers ---

func logHandlerOptions(level string) *slog.HandlerOptions {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return &slog.HandlerOptions{Level: l}
}

func makeLogger(level, pluginFilter string) *slog.Logger {
	h := clog.New(os.Stderr, logHandlerOptions(level))
	if pluginFilter != "" {
		return slog.New(clog.NewPluginFilter(h, pluginFilter))
	}
	return slog.New(h)
}

// dbPath returns the SQLite store path for the given config file:
// pipeliner.db in the same directory as the config file.
func dbPath(cfgPath string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(cfgPath)), "pipeliner.db")
}

// buildTLSConfig returns the TLS configuration for the web server.
//
//   - selfSigned=true          → generate an in-memory self-signed cert
//   - certFile+keyFile non-empty → load the provided cert/key pair
//   - neither                  → return nil (plain HTTP, proxy mode)
func buildTLSConfig(selfSigned bool, certFile, keyFile string) (*tls.Config, string, error) {
	if certFile != "" && keyFile != "" {
		cfg, err := web.TLSConfigFromFiles(certFile, keyFile)
		return cfg, "", err
	}
	if selfSigned {
		cert, fp, err := web.GenerateSelfSigned()
		if err != nil {
			return nil, "", err
		}
		return &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}, fp, nil
	}
	return nil, "", nil
}
