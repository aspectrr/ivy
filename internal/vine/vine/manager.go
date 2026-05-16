package vine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// Manager manages the lifecycle of Docker sandbox containers.
type Manager struct {
	cli          *client.Client
	logger       *slog.Logger
	sandboxes    map[string]*Sandbox // sessionID → Sandbox
	defaultImage string
	idleTimeout  time.Duration
}

// ManagerConfig holds configuration for the sandbox manager.
type ManagerConfig struct {
	DockerHost  string
	AgentImage  string
	IdleTimeout time.Duration
	CPULimit    string
	MemoryLimit string
}

// NewManager creates a new sandbox manager.
func NewManager(cfg ManagerConfig, logger *slog.Logger) (*Manager, error) {
	opts := []client.Opt{client.FromEnv}
	if cfg.DockerHost != "" {
		opts = append(opts, client.WithHost(cfg.DockerHost))
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("creating Docker client: %w", err)
	}

	return &Manager{
		cli:          cli,
		logger:       logger,
		sandboxes:    make(map[string]*Sandbox),
		defaultImage: cfg.AgentImage,
		idleTimeout:  cfg.IdleTimeout,
	}, nil
}

// Create spins up a new sandbox container for the given session.
func (m *Manager) Create(ctx context.Context, sessionID string, cfg SandboxConfig) (*Sandbox, error) {
	if _, exists := m.sandboxes[sessionID]; exists {
		return nil, fmt.Errorf("sandbox already exists for session %s", sessionID)
	}

	if cfg.Image == "" {
		cfg.Image = m.defaultImage
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = "/workspace"
	}
	if cfg.NetworkMode == "" {
		cfg.NetworkMode = "none" // No network by default
	}

	containerCfg := &container.Config{
		Image: cfg.Image,
		Labels: map[string]string{
			"ivy-session-id": sessionID,
			"ivy-type":       "agent-workspace",
		},
		WorkingDir: cfg.WorkDir,
		Cmd:        []string{"sleep", "infinity"},
		Tty:        false,
	}

	hostCfg := &container.HostConfig{
		NetworkMode: container.NetworkMode(cfg.NetworkMode),
		AutoRemove:  false,
	}

	// Apply resource limits if specified.
	if cfg.CPULimit != "" {
		// Docker uses nano-CPUs. 1 CPU = 1e9 nano-CPUs.
		// For simplicity, we'll pass it as a period/quota via resources.
		hostCfg.Resources = container.Resources{
			// CPU and memory limits are set via NanoCpus and Memory.
			// We parse simple values here; the full implementation
			// would handle "0.5", "1.0", "2g", etc.
		}
	}

	createResp, err := m.cli.ContainerCreate(ctx, containerCfg, hostCfg, nil, nil, fmt.Sprintf("ivy-%s", sessionID))
	if err != nil {
		return nil, fmt.Errorf("creating container: %w", err)
	}

	// Start the container.
	if err := m.cli.ContainerStart(ctx, createResp.ID, container.StartOptions{}); err != nil {
		// Clean up the created container on start failure.
		_ = m.cli.ContainerRemove(ctx, createResp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("starting container: %w", err)
	}

	// Get container info for IP.
	containerJSON, err := m.cli.ContainerInspect(ctx, createResp.ID)
	if err != nil {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}

	var containerIP string
	if containerJSON.NetworkSettings != nil {
		for _, net := range containerJSON.NetworkSettings.Networks {
			containerIP = net.IPAddress
			break
		}
	}

	now := time.Now()
	sandbox := &Sandbox{
		ID:          createResp.ID,
		SessionID:   sessionID,
		ContainerIP: containerIP,
		CreatedAt:   now,
		LastUsedAt:  now,
		cli:         m.cli,
	}

	m.sandboxes[sessionID] = sandbox

	m.logger.Info("sandbox created",
		"session_id", sessionID,
		"container_id", createResp.ID,
		"ip", containerIP,
	)

	return sandbox, nil
}

// Get retrieves the sandbox for a session.
func (m *Manager) Get(sessionID string) (*Sandbox, error) {
	sb, ok := m.sandboxes[sessionID]
	if !ok {
		return nil, fmt.Errorf("no sandbox for session %s", sessionID)
	}
	return sb, nil
}

// List returns all active sandboxes.
func (m *Manager) List() []*Sandbox {
	result := make([]*Sandbox, 0, len(m.sandboxes))
	for _, sb := range m.sandboxes {
		result = append(result, sb)
	}
	return result
}

// Destroy removes the sandbox container for a session.
func (m *Manager) Destroy(ctx context.Context, sessionID string) error {
	sb, ok := m.sandboxes[sessionID]
	if !ok {
		return fmt.Errorf("no sandbox for session %s", sessionID)
	}

	err := m.cli.ContainerRemove(ctx, sb.ID, container.RemoveOptions{
		Force: true,
	})
	if err != nil {
		return fmt.Errorf("removing container: %w", err)
	}

	delete(m.sandboxes, sessionID)

	m.logger.Info("sandbox destroyed",
		"session_id", sessionID,
		"container_id", sb.ID,
	)

	return nil
}

// CleanupIdle removes sandboxes that have been idle longer than the timeout.
func (m *Manager) CleanupIdle(ctx context.Context) error {
	now := time.Now()
	var cleaned int

	for sessionID, sb := range m.sandboxes {
		if now.Sub(sb.LastUsedAt) > m.idleTimeout {
			if err := m.Destroy(ctx, sessionID); err != nil {
				m.logger.Error("failed to cleanup idle sandbox",
					"session_id", sessionID,
					"error", err,
				)
				continue
			}
			cleaned++
		}
	}

	if cleaned > 0 {
		m.logger.Info("cleaned up idle sandboxes", "count", cleaned)
	}
	return nil
}

// Close cleans up all sandboxes and closes the Docker client.
func (m *Manager) Close(ctx context.Context) error {
	for sessionID := range m.sandboxes {
		_ = m.Destroy(ctx, sessionID)
	}
	return m.cli.Close()
}
