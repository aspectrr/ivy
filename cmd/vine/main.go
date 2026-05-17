// vine is the main Ivy daemon — the vine.
// It manages agent sessions, Docker sandboxes, and orchestrates
// the agent runtime. Leaf daemons connect to it via gRPC.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aspectrr/ivy/internal/ivyv1"
	"github.com/aspectrr/ivy/internal/vine/config"
	"github.com/aspectrr/ivy/internal/vine/connector/clickup"
	"github.com/aspectrr/ivy/internal/vine/database"
	"github.com/aspectrr/ivy/internal/vine/embed"
	"github.com/aspectrr/ivy/internal/vine/eventstore"
	"github.com/aspectrr/ivy/internal/vine/history"
	"github.com/aspectrr/ivy/internal/vine/leafmgr"
	"github.com/aspectrr/ivy/internal/vine/model"
	"github.com/aspectrr/ivy/internal/vine/orchestrator"
	"github.com/aspectrr/ivy/internal/vine/session"
	"github.com/aspectrr/ivy/internal/vine/skills"
	"github.com/aspectrr/ivy/internal/vine/tools"
	"github.com/aspectrr/ivy/internal/vine/vine"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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

	// ── Database ──────────────────────────────────────────────
	pool, err := database.NewPool(ctx, cfg.Database)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if cfg.LLM.EmbeddingDim > 0 {
		if err := database.EnsureEmbeddingDim(ctx, pool, cfg.LLM.EmbeddingDim); err != nil {
			slog.Error("failed to set embedding dimension", "error", err, "dim", cfg.LLM.EmbeddingDim)
			os.Exit(1)
		}
		slog.Info("embedding dimension configured", "dim", cfg.LLM.EmbeddingDim)
	}

	// ── Embedding client ──────────────────────────────────────
	embedClient := embed.NewClient(cfg.LLM)

	// ── Stores ────────────────────────────────────────────────
	sessions := session.NewStore(pool)
	events := eventstore.NewStore(pool)
	skillsStore := skills.NewStore(pool, embedClient)
	historyStore := history.NewStore(pool, embedClient)

	// ── Leaf manager & gRPC server ────────────────────────────
	leafManager := leafmgr.NewManager(logger)

	grpcServer, err := newGRPCServer(cfg)
	if err != nil {
		slog.Error("failed to create gRPC server", "error", err)
		os.Exit(1)
	}
	ivyv1.RegisterLeafServiceServer(grpcServer, leafmgr.NewLeafServiceServer(leafManager, logger))

	grpcAddr := fmt.Sprintf(":%d", cfg.Server.GRPCPort)
	grpcLis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		slog.Error("failed to listen on gRPC port", "error", err, "port", cfg.Server.GRPCPort)
		os.Exit(1)
	}

	go func() {
		slog.Info("gRPC server listening", "addr", grpcAddr)
		if err := grpcServer.Serve(grpcLis); err != nil {
			slog.Error("gRPC server error", "error", err)
		}
	}()

	// ── Sandbox manager ───────────────────────────────────────
	sandboxMgr, err := vine.NewManager(vine.ManagerConfig{
		DockerHost:  cfg.Sandbox.DockerHost,
		AgentImage:  cfg.Sandbox.AgentImage,
		IdleTimeout: cfg.Sandbox.IdleTimeout,
		CPULimit:    cfg.Sandbox.CPULimit,
		MemoryLimit: cfg.Sandbox.MemoryLimit,
	}, logger)
	if err != nil {
		slog.Error("failed to create sandbox manager", "error", err)
		os.Exit(1)
	}

	// ── Pipeline manager ──────────────────────────────────────
	pipelineMgr, err := vine.NewPipelineManager(cfg.Sandbox.DockerHost, logger)
	if err != nil {
		slog.Error("failed to create pipeline manager", "error", err)
		os.Exit(1)
	}

	// ── Tool registry ─────────────────────────────────────────
	registry := tools.NewRegistry()

	// Sandbox tools (bash, read_file, write_file)
	if err := tools.RegisterSandboxTools(registry); err != nil {
		slog.Error("failed to register sandbox tools", "error", err)
		os.Exit(1)
	}

	// Parser host tools (exec on remote leaf daemons)
	if err := tools.RegisterParserTools(registry, leafManager); err != nil {
		slog.Error("failed to register parser tools", "error", err)
		os.Exit(1)
	}

	// Pipeline tools
	if err := tools.RegisterPipelineTools(registry, pipelineProviderAdapter{mgr: pipelineMgr}); err != nil {
		slog.Error("failed to register pipeline tools", "error", err)
		os.Exit(1)
	}

	// Search tools (history + skills)
	skillsAdapter := &tools.SkillsStoreAdapter{Store: skillsStore}
	historyAdapter := &tools.HistoryStoreAdapter{Store: historyStore}
	if err := tools.RegisterSearchTools(registry, skillsAdapter, skillsAdapter, historyAdapter); err != nil {
		slog.Error("failed to register search tools", "error", err)
		os.Exit(1)
	}

	// Skill tools (list, get, create)
	if err := tools.RegisterSkillTools(registry, &skillsToolStoreAdapter{store: skillsStore}); err != nil {
		slog.Error("failed to register skill tools", "error", err)
		os.Exit(1)
	}

	// ClickUp tools
	if cfg.Connectors.ClickUp.Enabled {
		clickupClient, err := clickup.NewClient(cfg.Connectors.ClickUp, logger)
		if err != nil {
			slog.Error("failed to create ClickUp client", "error", err)
			os.Exit(1)
		}
		if err := tools.RegisterClickUpTools(registry, clickupClient); err != nil {
			slog.Error("failed to register ClickUp tools", "error", err)
			os.Exit(1)
		}
		slog.Info("ClickUp connector enabled")
	}

	slog.Info("tools registered", "count", len(registry.List()))

	// ── Orchestrator ──────────────────────────────────────────
	llmClient := orchestrator.NewLLMClient(cfg.LLM)

	orchestratorInst := orchestrator.New(
		sessions,
		events,
		llmClient,
		&toolExecutorAdapter{registry: registry},
		logger,
	)

	// ── ClickUp poller ────────────────────────────────────────
	var clickupPoller *clickup.Poller
	if cfg.Connectors.ClickUp.Enabled && cfg.Connectors.ClickUp.PollInterval > 0 {
		clickupClient, err := clickup.NewClient(cfg.Connectors.ClickUp, logger)
		if err != nil {
			slog.Error("failed to create ClickUp client for poller", "error", err)
			os.Exit(1)
		}

		clickupPoller = clickup.NewPoller(clickupClient, cfg.Connectors.ClickUp, func(task clickup.Task, isNew bool) {
			sourceID := task.ID
			slog.Info("clickup task event",
				"task_id", sourceID,
				"is_new", isNew,
				"name", task.Name,
			)

			if isNew {
				// Create a new session for this ClickUp task.
				sess, err := sessions.Create(ctx, "clickup", sourceID, json.RawMessage(`{}`))
				if err != nil {
					slog.Error("failed to create session for clickup task",
						"task_id", sourceID,
						"error", err,
					)
					return
				}
				slog.Info("created session for clickup task",
					"session_id", sess.ID,
					"task_id", sourceID,
				)

				// Seed the session with the task details as the initial user message.
				description := task.Description
				if description == "" {
					description = "(no description)"
				}
				taskContext := fmt.Sprintf("[ClickUp Task: %s]\nURL: %s\nStatus: %s\n\n%s",
					task.Name, task.URL, task.Status.Status, description)

				if _, err := events.Append(ctx, sess.ID, model.EventTypeUserMessage, mustJSON(model.UserMessagePayload{
					Content: taskContext,
				})); err != nil {
					slog.Error("failed to seed user message for clickup session",
						"session_id", sess.ID,
						"error", err,
					)
					return
				}

				// Start the agent run.
				if err := orchestratorInst.StartRun(ctx, sess.ID); err != nil {
					slog.Error("failed to start run for clickup session",
						"session_id", sess.ID,
						"error", err,
					)
					return
				}
				slog.Info("started agent run for clickup task",
					"session_id", sess.ID,
					"task_id", sourceID,
				)
			} else {
				// Task was updated — try to resume an existing session.
				sess, err := sessions.GetBySource(ctx, "clickup", sourceID)
				if err != nil {
					slog.Error("failed to lookup session for updated clickup task",
						"task_id", sourceID,
						"error", err,
					)
					return
				}

				if sess.Status == model.StatusRunning {
					// Already running — append the update as a new user message
					// so the agent loop picks it up on the next turn.
					updateMsg := fmt.Sprintf("[ClickUp Task Updated: %s]\nStatus: %s\nURL: %s",
						task.Name, task.Status.Status, task.URL)
					if _, err := events.Append(ctx, sess.ID, model.EventTypeUserMessage, mustJSON(model.UserMessagePayload{
						Content: updateMsg,
					})); err != nil {
						slog.Error("failed to append update message to running session",
							"session_id", sess.ID,
							"error", err,
						)
					}
					slog.Info("appended update to running session",
						"session_id", sess.ID,
						"task_id", sourceID,
					)
					return
				}

				if sess.Status == model.StatusSuspended || sess.Status == model.StatusFailed {
					// Resume the session with context about the task update.
					updateMsg := fmt.Sprintf("[ClickUp Task Updated: %s]\nStatus: %s\nURL: %s\n\nResuming session due to task update.",
						task.Name, task.Status.Status, task.URL)
					if _, err := events.Append(ctx, sess.ID, model.EventTypeUserMessage, mustJSON(model.UserMessagePayload{
						Content: updateMsg,
					})); err != nil {
						slog.Error("failed to append resume message to session",
							"session_id", sess.ID,
							"error", err,
						)
						return
					}

					if err := orchestratorInst.Resume(ctx, sess.ID); err != nil {
						slog.Error("failed to resume session for updated clickup task",
							"session_id", sess.ID,
							"error", err,
						)
						return
					}
					slog.Info("resumed session for updated clickup task",
						"session_id", sess.ID,
						"task_id", sourceID,
					)
					return
				}

				// Session is completed or otherwise not resumable — log and skip.
				slog.Info("skipping update for non-resumable session",
					"session_id", sess.ID,
					"task_id", sourceID,
					"status", sess.Status,
				)
			}
		}, logger)
		clickupPoller.Start(ctx)
		slog.Info("ClickUp poller started", "interval", cfg.Connectors.ClickUp.PollInterval)
	}

	// ── HTTP server (webhooks + health) ───────────────────────
	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"ok","grpc_port":%d}`, cfg.Server.GRPCPort)
	})

	if cfg.Connectors.ClickUp.Enabled && cfg.Connectors.ClickUp.WebhookSecret != "" {
		httpMux.HandleFunc("/webhooks/clickup", newClickUpWebhookHandler(cfg.Connectors.ClickUp.WebhookSecret, logger))
		slog.Info("ClickUp webhook endpoint enabled", "path", "/webhooks/clickup")
	}

	httpAddr := fmt.Sprintf(":%d", cfg.Server.HTTPPort)
	httpServer := &http.Server{
		Addr:              httpAddr,
		Handler:           httpMux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("HTTP server listening", "addr", httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "error", err)
		}
	}()

	slog.Info("vine started successfully")

	// ── Wait for shutdown signal ──────────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		slog.Info("received shutdown signal", "signal", sig)
	case <-ctx.Done():
		slog.Info("context cancelled")
	}

	// ── Graceful shutdown ─────────────────────────────────────
	slog.Info("vine shutting down gracefully")

	// Stop ClickUp poller
	if clickupPoller != nil {
		clickupPoller.Stop()
	}

	// Stop HTTP server
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	if err := httpServer.Shutdown(shutCtx); err != nil {
		slog.Error("HTTP server shutdown error", "error", err)
	}

	// Stop gRPC server
	grpcServer.GracefulStop()

	// Destroy sandboxes
	if err := sandboxMgr.Close(shutCtx); err != nil {
		slog.Error("sandbox manager shutdown error", "error", err)
	}

	// Destroy pipeline sandboxes
	if err := pipelineMgr.Close(shutCtx); err != nil {
		slog.Error("pipeline manager shutdown error", "error", err)
	}

	slog.Info("vine shutdown complete")
}

// newGRPCServer creates a gRPC server with optional TLS.
func newGRPCServer(cfg *config.Config) (*grpc.Server, error) {
	if cfg.Server.GRPCTLS.Cert == "" {
		return grpc.NewServer(), nil
	}

	cert, err := tls.LoadX509KeyPair(cfg.Server.GRPCTLS.Cert, cfg.Server.GRPCTLS.Key)
	if err != nil {
		return nil, fmt.Errorf("loading gRPC TLS certificate: %w", err)
	}

	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}

	if cfg.Server.GRPCTLS.CA != "" {
		caData, err := os.ReadFile(cfg.Server.GRPCTLS.CA)
		if err != nil {
			return nil, fmt.Errorf("reading gRPC CA certificate: %w", err)
		}
		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caData) {
			return nil, fmt.Errorf("failed to append CA certificate")
		}
		tlsCfg.ClientCAs = caPool
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg))), nil
}

// newClickUpWebhookHandler returns an HTTP handler for ClickUp webhook events.
func newClickUpWebhookHandler(secret string, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Verify the webhook signature if secret is configured.
		if sig := r.Header.Get("X-Signature"); sig != "" && secret != "" {
			// In production, compute HMAC of the body with the secret and compare.
			// For now, just log the incoming webhook.
			logger.Info("clickup webhook received", "signature_present", true)
		} else {
			logger.Warn("clickup webhook missing signature")
		}

		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"status":"received"}`)
	}
}

// mustJSON marshals v to JSON, returning an empty object on error.
func mustJSON(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

// ── Adapter types ────────────────────────────────────────────

// toolExecutorAdapter adapts tools.Registry to orchestrator.ToolExecutor.
type toolExecutorAdapter struct {
	registry *tools.Registry
}

func (a *toolExecutorAdapter) ExecuteTool(ctx context.Context, name string, args json.RawMessage, sessionID string) (json.RawMessage, error) {
	return a.registry.Execute(ctx, name, args, tools.ToolContext{
		SessionID: sessionID,
	})
}

// pipelineProviderAdapter adapts vine.PipelineManager to tools.PipelineProvider.
type pipelineProviderAdapter struct {
	mgr *vine.PipelineManager
}

func (a pipelineProviderAdapter) GetPipeline(sessionID string) (*vine.PipelineSandbox, error) {
	return a.mgr.Get(sessionID)
}

// skillsToolStoreAdapter adapts skills.Store to tools.SkillStore.
type skillsToolStoreAdapter struct {
	store *skills.Store
}

func (a *skillsToolStoreAdapter) ListSkills(ctx context.Context) ([]tools.SkillSummary, error) {
	results, err := a.store.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tools.SkillSummary, len(results))
	for i, r := range results {
		out[i] = tools.SkillSummary{
			Name:        r.Name,
			Description: r.Description,
			BuiltIn:     r.BuiltIn,
		}
	}
	return out, nil
}

func (a *skillsToolStoreAdapter) GetSkill(ctx context.Context, name string) (*tools.SkillContent, error) {
	skill, err := a.store.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	return &tools.SkillContent{
		Name:        skill.Name,
		Description: skill.Description,
		Content:     skill.Content,
		BuiltIn:     skill.BuiltIn,
	}, nil
}
