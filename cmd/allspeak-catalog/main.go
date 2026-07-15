package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pkarpovich/allspeak-catalog/internal/api"
	"github.com/pkarpovich/allspeak-catalog/internal/blob"
	"github.com/pkarpovich/allspeak-catalog/internal/store"
)

const shutdownTimeout = 15 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	catalog, err := store.New(cfg.dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = catalog.Close() }()

	r2, err := blob.New(blob.Config{
		Endpoint: cfg.cfEndpoint,
		KeyID:    cfg.cfKeyID,
		Secret:   cfg.cfSecret,
		Bucket:   cfg.cfBucket,
	})
	if err != nil {
		return fmt.Errorf("init blob client: %w", err)
	}

	server := api.NewServer(api.Config{
		AdminToken: cfg.adminToken,
		ReadToken:  cfg.readToken,
		Store:      catalog,
		Blob:       r2,
		Logger:     logger,
	})

	httpServer := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.listenAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
