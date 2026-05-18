package leafmgr

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/aspectrr/ivy/internal/ivyv1"
)

// LeafConnection represents a connected leaf daemon.
type LeafConnection struct {
	HostID      string
	Hostname    string
	AllowedDirs []string
	Stream      ivyv1.LeafService_ConnectServer
}

// PendingCommand tracks a command sent to a leaf, waiting for a response.
type PendingCommand struct {
	Response chan *ivyv1.CommandOutput
}

// Manager tracks connected leaf daemons and routes commands to them.
type Manager struct {
	mu      sync.RWMutex
	leaves  map[string]*LeafConnection // hostID → connection
	pending map[string]*PendingCommand // requestID → pending response
	logger  *slog.Logger
}

// NewManager creates a new leaf connection manager.
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{
		leaves:  make(map[string]*LeafConnection),
		pending: make(map[string]*PendingCommand),
		logger:  logger,
	}
}

// RegisterLeaf adds a newly connected leaf.
func (m *Manager) RegisterLeaf(conn *LeafConnection) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// If this leaf was already connected, close the old connection
	if old, exists := m.leaves[conn.HostID]; exists {
		m.logger.Warn("replacing existing leaf connection",
			"host_id", conn.HostID,
			"old_hostname", old.Hostname,
			"new_hostname", conn.Hostname,
		)
	}

	m.leaves[conn.HostID] = conn
	m.logger.Info("leaf registered",
		"host_id", conn.HostID,
		"hostname", conn.Hostname,
		"allowed_dirs", conn.AllowedDirs,
	)
}

// UnregisterLeaf removes a leaf (disconnected).
func (m *Manager) UnregisterLeaf(hostID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.leaves[hostID]; exists {
		delete(m.leaves, hostID)
		m.logger.Info("leaf unregistered", "host_id", hostID)
	}
}

// GetLeaf returns a leaf connection by host ID.
func (m *Manager) GetLeaf(hostID string) (*LeafConnection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	conn, ok := m.leaves[hostID]
	if !ok {
		return nil, fmt.Errorf("leaf %q is not connected", hostID)
	}
	return conn, nil
}

// LeafInfo holds summary information about a connected leaf daemon.
type LeafInfo struct {
	HostID      string   `json:"host_id"`
	Hostname    string   `json:"hostname"`
	AllowedDirs []string `json:"allowed_dirs"`
}

// ListLeaves returns all connected leaf IDs.
func (m *Manager) ListLeaves() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.leaves))
	for id := range m.leaves {
		ids = append(ids, id)
	}
	return ids
}

// ListConnectedLeaves returns summary information for all connected leaf daemons.
func (m *Manager) ListConnectedLeaves() []LeafInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]LeafInfo, 0, len(m.leaves))
	for _, conn := range m.leaves {
		infos = append(infos, LeafInfo{
			HostID:      conn.HostID,
			Hostname:    conn.Hostname,
			AllowedDirs: conn.AllowedDirs,
		})
	}
	return infos
}

// SendCommand sends a command to a leaf and registers a pending response.
// Returns the requestID for tracking.
func (m *Manager) SendCommand(ctx context.Context, hostID string, req *ivyv1.ExecuteCommandRequest) (*PendingCommand, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	conn, ok := m.leaves[hostID]
	if !ok {
		return nil, fmt.Errorf("leaf %q is not connected", hostID)
	}

	pending := &PendingCommand{
		Response: make(chan *ivyv1.CommandOutput, 1),
	}
	m.pending[req.RequestId] = pending

	// Send the command through the stream
	vineCmd := &ivyv1.VineCommand{
		Payload: &ivyv1.VineCommand_ExecuteCommand{
			ExecuteCommand: req,
		},
	}
	if err := conn.Stream.Send(vineCmd); err != nil {
		delete(m.pending, req.RequestId)
		return nil, fmt.Errorf("sending command to leaf %q: %w", hostID, err)
	}

	return pending, nil
}

// SendCommandAndWait sends a command to a leaf and waits for the response.
// hostID must be non-empty — use the list_parser_hosts tool to discover available hosts.
// This is the main interface used by parser host tools.
func (m *Manager) SendCommandAndWait(ctx context.Context, hostID string, req *ivyv1.ExecuteCommandRequest) (*ivyv1.CommandOutput, error) {
	if hostID == "" {
		return nil, fmt.Errorf("host_id is required: no default leaf host")
	}

	pending, err := m.SendCommand(ctx, hostID, req)
	if err != nil {
		return nil, err
	}

	select {
	case resp := <-pending.Response:
		return resp, nil
	case <-ctx.Done():
		// Clean up pending entry
		m.mu.Lock()
		delete(m.pending, req.RequestId)
		m.mu.Unlock()
		return nil, fmt.Errorf("command timed out: %w", ctx.Err())
	}
}

// HandleCommandOutput routes a command response from a leaf to the
// pending command channel.
func (m *Manager) HandleCommandOutput(output *ivyv1.CommandOutput) {
	m.mu.Lock()
	pending, ok := m.pending[output.RequestId]
	if ok {
		delete(m.pending, output.RequestId)
	}
	m.mu.Unlock()

	if !ok {
		m.logger.Warn("received command output for unknown request",
			"request_id", output.RequestId,
		)
		return
	}

	pending.Response <- output
}

// SendSyncRequest sends a directory sync request to a leaf.
func (m *Manager) SendSyncRequest(ctx context.Context, hostID string, req *ivyv1.SyncDirectoryRequest) error {
	m.mu.RLock()
	conn, ok := m.leaves[hostID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("leaf %q is not connected", hostID)
	}

	vineCmd := &ivyv1.VineCommand{
		Payload: &ivyv1.VineCommand_SyncDirectory{
			SyncDirectory: req,
		},
	}
	return conn.Stream.Send(vineCmd)
}
