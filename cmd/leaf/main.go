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
	"time"

	"github.com/aspectrr/ivy/internal/leaf/commands"
	"github.com/aspectrr/ivy/internal/leaf/config"
	"github.com/aspectrr/ivy/internal/leaf/grpcclient"
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

	// Set log level from config
	if cfg.LogLevel == "debug" {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
		slog.SetDefault(logger)
	}

	hostname, _ := os.Hostname()

	slog.Info("leaf starting",
		"vine_address", cfg.Vine.Address,
		"allowed_dirs", cfg.AllowedDirectories,
		"allowed_cmds", cfg.AllowedCommands,
		"hostname", hostname,
	)

	// Initialize command executor
	executor := commands.NewExecutor(cfg.AllowedDirectories, cfg.CommandTimeout)

	// Initialize gRPC client
	client := grpcclient.NewClient(grpcclient.ClientConfig{
		VineAddress:       cfg.Vine.Address,
		HostID:            hostname,
		Hostname:          hostname,
		AllowedDirs:       cfg.AllowedDirectories,
		ReconnectInterval: cfg.Vine.ReconnectInterval,
		TLSCert:           cfg.Vine.TLS.Cert,
		TLSKey:            cfg.Vine.TLS.Key,
		TLSCA:             cfg.Vine.TLS.CA,
	}, executor, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run gRPC client in background
	go func() {
		if err := client.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Error("gRPC client exited with error", "error", err)
		}
	}()

	// Heartbeat goroutine
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if client.IsConnected() {
					if err := client.SendHeartbeat(); err != nil {
						slog.Warn("heartbeat failed", "error", err)
					}
				}
			}
		}
	}()

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
	if err := client.Close(); err != nil {
		slog.Error("error closing client", "error", err)
	}
}
