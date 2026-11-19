package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ppablomunoz/coda/internal/config"
	"github.com/ppablomunoz/coda/internal/musicbrainz"
	"github.com/ppablomunoz/coda/internal/server"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/tasks"
	"github.com/ppablomunoz/coda/internal/utils"
)

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
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
		UserAgent: "Coda/0.0.1 ( pablomunoz@pablomunoz.dev )",
		Logger:    log,
	})
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}

	queue := tasks.NewQueue(log)
	runner := tasks.NewRunner(st, mb, log, queue)
	scheduler := tasks.NewScheduler(queue, log)

	// Start server
	handler := server.NewHandler(log, st, mb, queue)
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Infof("server listening on :%d", cfg.Port)
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
