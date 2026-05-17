package leafmgr

import (
	"io"
	"log/slog"

	"github.com/aspectrr/ivy/internal/ivyv1"
)

// LeafServiceServer implements the gRPC LeafService for vine.
type LeafServiceServer struct {
	ivyv1.UnimplementedLeafServiceServer
	manager *Manager
	logger  *slog.Logger
}

// NewLeafServiceServer creates a new gRPC server handler.
func NewLeafServiceServer(manager *Manager, logger *slog.Logger) *LeafServiceServer {
	return &LeafServiceServer{
		manager: manager,
		logger:  logger,
	}
}

// Connect handles the bidirectional stream from a leaf daemon.
// The leaf sends messages (registration, heartbeats, command output, directory chunks).
// The vine sends commands through this stream.
func (s *LeafServiceServer) Connect(stream ivyv1.LeafService_ConnectServer) error {
	var hostID string

	defer func() {
		if hostID != "" {
			s.manager.UnregisterLeaf(hostID)
		}
	}()

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		switch payload := msg.Payload.(type) {
		case *ivyv1.LeafMessage_Registration:
			reg := payload.Registration
			hostID = reg.HostId

			conn := &LeafConnection{
				HostID:      reg.HostId,
				Hostname:    reg.Hostname,
				AllowedDirs: reg.AllowedDirectories,
				Stream:      stream,
			}
			s.manager.RegisterLeaf(conn)

			s.logger.Info("leaf connected",
				"host_id", reg.HostId,
				"hostname", reg.Hostname,
			)

		case *ivyv1.LeafMessage_Heartbeat:
			// Heartbeat acknowledged — connection is alive
			s.logger.Debug("heartbeat from leaf", "host_id", hostID)

		case *ivyv1.LeafMessage_CommandOutput:
			s.manager.HandleCommandOutput(payload.CommandOutput)

		case *ivyv1.LeafMessage_DirectoryChunk:
			// TODO: Handle directory chunks for incremental sync (Phase 4+)
			s.logger.Debug("directory chunk from leaf",
				"host_id", hostID,
				"request_id", payload.DirectoryChunk.RequestId,
			)

		default:
			s.logger.Warn("unknown message from leaf", "host_id", hostID)
		}
	}
}
