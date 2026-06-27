package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ihomeland/server/internal/app"
	"ihomeland/server/internal/config"
	"ihomeland/server/internal/logger"
)

const shutdownTimeout = 10 * time.Second

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}

	log, err := logger.New(cfg.LogLevel)
	if err != nil {
		slog.Error("init logger failed", "error", err)
		os.Exit(1)
	}
	slog.SetDefault(log)

	server, err := app.NewHTTPServer(cfg, log)
	if err != nil {
		log.Error("create http server failed", "error", err)
		os.Exit(1)
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server starting", "addr", cfg.HTTPAddr)
		errCh <- server.ListenAndServe()
	}()

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-stopCh:
		log.Info("shutdown signal received", "signal", sig.String())
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server failed", "error", err)
			os.Exit(1)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Error("http server shutdown failed", "error", err)
		os.Exit(1)
	}
	log.Info("http server stopped")
}
