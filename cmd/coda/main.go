package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ppablomunoz/coda/internal/artwork"
	"github.com/ppablomunoz/coda/internal/config"
	"github.com/ppablomunoz/coda/internal/lock"
	"github.com/ppablomunoz/coda/internal/musicbrainz"
	"github.com/ppablomunoz/coda/internal/server"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/tasks"
	"github.com/ppablomunoz/coda/internal/utils"
)

// version is stamped at release time via:
// go build -ldflags "-X main.version=0.1.0" ./cmd/coda
// Local builds report "dev".
var version = "dev"

// main wires config, DB, MusicBrainz, artwork, queue/runner/scheduler, and the
// HTTP server, then runs until interrupt with a 10s graceful shutdown.
func main() {
	configPath := flag.String(
		"config",
		"",
		"config file path (overrides CODA_CONFIG)",
	)
	checkConfig := flag.Bool(
		"check-config",
		false,
		"print resolved config and exit without starting",
	)
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: coda [flags]\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(flag.CommandLine.Output(), "\nConfig file (first run creates it):\n")
		fmt.Fprintf(
			flag.CommandLine.Output(),
			"  macOS: ~/Library/Application Support/Coda/config.yaml\n",
		)
		fmt.Fprintf(flag.CommandLine.Output(), "  Linux: ~/.config/coda/config.yaml\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Env overrides:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  CODA_CONFIG  config file path (or --config)\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  CODA_DB_PATH database file path\n")
		fmt.Fprintf(
			flag.CommandLine.Output(),
			"  CODA_HOST    listen address: IP or hostname, empty = all interfaces\n",
		)
		fmt.Fprintf(
			flag.CommandLine.Output(),
			"  CODA_PORT    server port (1-65535)\n",
		)
		fmt.Fprintf(
			flag.CommandLine.Output(),
			"  CODA_ROOTS   OS path list of music dirs (: on macOS/Linux, ; on Windows)\n",
		)
		fmt.Fprintf(
			flag.CommandLine.Output(),
			"  CODA_ALLOWED_HOSTS  comma-separated extra Host names (Host allowlist)\n",
		)
		fmt.Fprintf(
			flag.CommandLine.Output(),
			"  CODA_SCAN_TIMEOUT  scan bound as Go duration (e.g. 15m, default 10m)\n",
		)
		fmt.Fprintf(
			flag.CommandLine.Output(),
			"  CODA_DEBUG   1/true/yes/on enables debug logs\n",
		)
		fmt.Fprintf(
			flag.CommandLine.Output(),
			"  CODA_LOG_LEVEL  \"debug\" also enables debug logs\n",
		)
		fmt.Fprintf(
			flag.CommandLine.Output(),
			"  GIN_MODE     \"debug\" opts back into Gin's debug route dump\n",
		)
		fmt.Fprintf(flag.CommandLine.Output(), "\nExamples:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  coda --version\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  coda --help\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  coda --check-config\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  coda --config /path/to/config.yaml\n")
	}
	flag.Parse()
	if *showVersion {
		fmt.Printf("coda %s\n", version)
		return
	}
	if *configPath != "" {
		_ = os.Setenv("CODA_CONFIG", *configPath)
	}
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	log := utils.New()
	log.Info("Starting coda...")

	// Read cfg
	cfg, err := config.GetConfig(log)
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
	log.Infof("config loaded: listen=%s roots=%d", cfg.ListenAddr(), len(cfg.Roots))
	log.Debugf("config roots: %q", cfg.Roots)

	dsn, err := config.GetDBPath()
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
	dbPath, _, _ := strings.Cut(dsn, "?")
	log.Infof("db dir: %s", filepath.Dir(dbPath))
	log.Debugf("db file: %s", dbPath)

	if *checkConfig {
		fmt.Printf("coda %s\n", version)
		if cfg.Host == "" {
			fmt.Printf("host: (all interfaces)\n")
		} else {
			fmt.Printf("host: %s\n", cfg.Host)
		}
		fmt.Printf("port: %d\n", cfg.Port)
		fmt.Printf("listen: %s\n", cfg.ListenAddr())
		fmt.Printf("roots: %d\n", len(cfg.Roots))
		for _, r := range cfg.Roots {
			fmt.Printf("  - %s\n", r)
		}
		fmt.Printf("allowed_hosts: %d\n", len(cfg.AllowedHosts))
		for _, h := range cfg.AllowedHosts {
			fmt.Printf("  - %s\n", h)
		}
		fmt.Printf("scan_timeout: %s\n", cfg.ScanTimeout)
		fmt.Printf("db: %s\n", dbPath)
		fmt.Printf("covers: %s\n", config.CoverDirFromDBPath(dsn))
		fmt.Printf("lock: %s\n", lock.PathForDB(dsn))
		return
	}

	// Single-instance guard: a second process on the same data dir would
	// scan, move, and tag the same files concurrently. Fail fast instead.
	lockHandle, err := lock.Acquire(dsn)
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
	defer func() { _ = lockHandle.Release() }()

	st, err := store.NewStore(dsn, cfg.Roots, log)
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}

	defer st.Close()

	// Read library roots dirs
	if err := st.AddRootDirs(ctx); err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}

	mb, err := musicbrainz.New(musicbrainz.Config{
		UserAgent: fmt.Sprintf("Coda/%s ( https://github.com/PPabloMunoz/coda )", version),
		Logger:    log,
	})
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}

	coverDir := config.CoverDirFromDBPath(dsn)
	art, err := artwork.New(artwork.Config{
		UserAgent: fmt.Sprintf("Coda/%s ( https://github.com/PPabloMunoz/coda )", version),
		Logger:    log,
	})
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}

	queue := tasks.NewQueue(log, st)
	runner := tasks.NewRunner(st, mb, log, queue, art, coverDir)
	runner.SetScanTimeout(cfg.ScanTimeout)
	scheduler := tasks.NewScheduler(queue, log)

	// Recover durable intent from before a restart: interrupted imports go
	// back on the queue, pending cover/relink jobs return, and terminal
	// import states hydrate so the status is not reset to idle.
	if err := queue.Recover(ctx); err != nil {
		log.Errorf("recover pending tasks: %v", err)
	}

	// Start server
	addr := cfg.ListenAddr()
	// Gin defaults to debug mode (route table dump + warnings on stdout).
	// Release is the default for the shipped binary; developers can still
	// opt back into debug output with GIN_MODE=debug.
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	router := server.NewHandler(log, st, mb, queue, art, coverDir, server.Options{
		AllowedHosts: cfg.AllowedHosts,
		ListenHost:   cfg.Host,
	})
	srv := &http.Server{
		Addr:              addr,
		Handler:           router.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       90 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Infof("server listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	var wg sync.WaitGroup
	wg.Go(func() { runner.Start(ctx) })
	wg.Go(func() { scheduler.Start(ctx) })

	queue.EnqueueScan(false)

	// Graceful shutdown
	select {
	case <-ctx.Done():
	case err = <-errCh:
		log.Error(err.Error())
	}

	log.Info("shutting down...")
	shutCtx, cancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	wg.Wait()

	if err != nil {
		os.Exit(1)
	}
}
