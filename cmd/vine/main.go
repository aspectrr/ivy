// vine is the main Ivy daemon — the vine.
// It manages agent sessions, Docker sandboxes, and orchestrates
// the agent runtime. Leaf daemons connect to it via gRPC.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/aspectrr/ivy/internal/vine/config"
)

func main() {
	configPath := flag.String("config", "configs/vine.yaml", "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	slog.Info("vine starting",
		"grpc_port", cfg.Server.GRPCPort,
		"http_port", cfg.Server.HTTPPort,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// TODO: Initialize database connection pool
	// TODO: Initialize gRPC server
	// TODO: Initialize HTTP server (for ClickUp webhooks)
	// TODO: Initialize sandbox manager
	// TODO: Initialize orchestrator

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		slog.Info("received shutdown signal", "signal", sig)
	case <-ctx.Done():
		slog.Info("context cancelled")
	}

	slog.Info("vine shutting down gracefully")
	// TODO: Graceful shutdown — persist state, destroy sandboxes, close connections
}
