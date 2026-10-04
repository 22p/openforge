// Command openforge is a modern, ASU compatible OpenWrt firmware build portal.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/openforge/openforge/internal/config"
	"github.com/openforge/openforge/internal/server"
)

// Build metadata, injected with -ldflags at release time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	configPath := flag.String("config", "openforge.toml", "path to a TOML configuration file")
	listen := flag.String("listen", "", "override the configured listen address")
	showVersion := flag.Bool("version", false, "print version information and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("openforge %s (%s, %s)\n", version, commit, date)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "openforge: %v\n", err)
		os.Exit(1)
	}
	if *listen != "" {
		cfg.Listen = *listen
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLevel(cfg.LogLevel),
	}))

	srv, err := server.New(cfg, version, logger)
	if err != nil {
		logger.Error("failed to start server", "err", err)
		os.Exit(1)
	}
	defer srv.Close()

	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		WriteTimeout:      0, // Artifact downloads can be large; no hard cap.
		IdleTimeout:       90 * time.Second,
	}

	go func() {
		logger.Info("openforge listening",
			"addr", cfg.Listen,
			"upstream", cfg.UpstreamURL,
			"backend", displayBackend(cfg.BackendURL),
			"version", version,
		)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	logger.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
	}
}

func displayBackend(url string) string {
	if url == "" {
		return "(none)"
	}
	return url
}

func parseLevel(value string) slog.Level {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
