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
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("coda %s\n", version)
		return
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
	log.Infof("config loaded: port=%d roots=%d", cfg.Port, len(cfg.Roots))
	log.Debugf("config roots: %q", cfg.Roots)

	dsn, err := config.GetDBPath()
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
	dbPath, _, _ := strings.Cut(dsn, "?")
	log.Infof("db dir: %s", filepath.Dir(dbPath))
	log.Debugf("db file: %s", dbPath)

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
		UserAgent: fmt.Sprintf("Coda/%s ( pablomunoz@pablomunoz.dev )", version),
		Logger:    log,
	})
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}

	coverDir := config.CoverDirFromDBPath(dsn)
	art, err := artwork.New(artwork.Config{
		UserAgent: fmt.Sprintf("Coda/%s ( pablomunoz@pablomunoz.dev )", version),
		Logger:    log,
	})
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}

	queue := tasks.NewQueue(log)
	runner := tasks.NewRunner(st, mb, log, queue, art, coverDir)
	scheduler := tasks.NewScheduler(queue, log)

	// Start server
	addr := fmt.Sprintf(":%d", cfg.Port)
	// Gin defaults to debug mode (route table dump + warnings on stdout).
	// Release is the default for the shipped binary; developers can still
	// opt back into debug output with GIN_MODE=debug.
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	router := server.NewHandler(log, st, mb, queue, art, coverDir)
	srv := &http.Server{
		Addr:              addr,
		Handler:           router.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
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

	queue.EnqueueScan()

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
