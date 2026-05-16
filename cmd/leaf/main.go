// leaf is the Ivy leaf daemon.
// It runs on log parser hosts, executes whitelisted read-only commands,
// and syncs directory contents back to vine via gRPC.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/aspectrr/ivy/internal/leaf/config"
)

func main() {
	configPath := flag.String("config", "/etc/ivy-leaf/config.yaml", "path to config file")
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

	slog.Info("leaf starting",
		"vine_address", cfg.Vine.Address,
		"allowed_dirs", cfg.AllowedDirectories,
		"allowed_cmds", cfg.AllowedCommands,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// TODO: Initialize gRPC client connection to vine
	// TODO: Initialize command executor
	// TODO: Establish bidirectional stream

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		slog.Info("received shutdown signal", "signal", sig)
	case <-ctx.Done():
		slog.Info("context cancelled")
	}

	slog.Info("leaf shutting down gracefully")
}
