// Command server runs the TinyObs server.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/nicktill/tinyobs/pkg/server"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg := server.Config{
		Listen:             ":" + env("PORT", "8080"),
		DataDir:            env("TINYOBS_DATA_DIR", "./data/tinyobs-v2"),
		Retention:          envDuration(log, "TINYOBS_RETENTION", 72*time.Hour),
		MaxSeries:          int(envInt(log, "TINYOBS_MAX_SERIES", 50_000)),
		MaxSeriesPerMetric: int(envInt(log, "TINYOBS_MAX_SERIES_PER_METRIC", 10_000)),
		MemoryMB:           envInt(log, "TINYOBS_MAX_MEMORY_MB", 64),
		WebDir:             env("TINYOBS_WEB_DIR", "./web"),
		Logger:             log,
	}
	if _, err := os.Stat("./data/tinyobs"); err == nil && cfg.DataDir != "./data/tinyobs" {
		log.Info("V1 data in ./data/tinyobs is not read by V2 and can be deleted")
	}

	srv, err := server.New(cfg)
	if err != nil {
		log.Error("starting server", "err", err)
		os.Exit(1)
	}
	log.Info("TinyObs started", "version", server.Version, "data", cfg.DataDir, "retention", cfg.Retention, "max_series", cfg.MaxSeries, "max_series_per_metric", cfg.MaxSeriesPerMetric)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := srv.Run(ctx); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
	log.Info("TinyObs stopped")
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(log *slog.Logger, key string, def int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		log.Error("invalid environment variable", "key", key, "value", v)
		os.Exit(2)
	}
	return n
}

func envDuration(log *slog.Logger, key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		log.Error("invalid environment variable", "key", key, "value", v)
		os.Exit(2)
	}
	return d
}
