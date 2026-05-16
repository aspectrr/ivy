package vine

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// Sandbox represents a running Docker container that serves as an agent workspace.
type Sandbox struct {
	ID          string
	SessionID   string
	ContainerIP string
	CreatedAt   time.Time
	LastUsedAt  time.Time

	cli *client.Client
}

// SandboxConfig holds configuration for creating a new sandbox.
type SandboxConfig struct {
	Image       string
	CPULimit    string // e.g. "1.0"
	MemoryLimit string // e.g. "1g"
	NetworkMode string // e.g. "none", "bridge"
	WorkDir     string // e.g. "/workspace"
}

// ExecResult holds the result of a command executed inside a container.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Exec runs a command inside the sandbox container.
func (s *Sandbox) Exec(ctx context.Context, cmd string, args ...string) (*ExecResult, error) {
	s.LastUsedAt = time.Now()

	fullCmd := append([]string{cmd}, args...)

	execCfg := container.ExecOptions{
		Cmd:          fullCmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	execCreateResp, err := s.cli.ContainerExecCreate(ctx, s.ID, execCfg)
	if err != nil {
		return nil, fmt.Errorf("creating exec: %w", err)
	}

	attachResp, err := s.cli.ContainerExecAttach(ctx, execCreateResp.ID, container.ExecStartOptions{})
	if err != nil {
		return nil, fmt.Errorf("attaching exec: %w", err)
	}
	defer attachResp.Close()

	// Use stdcopy to properly demux Docker's multiplexed stream.
	var stdoutBuf, stderrBuf bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdoutBuf, &stderrBuf, attachResp.Reader); err != nil {
		return nil, fmt.Errorf("reading exec output: %w", err)
	}

	inspectResp, err := s.cli.ContainerExecInspect(ctx, execCreateResp.ID)
	if err != nil {
		return nil, fmt.Errorf("inspecting exec: %w", err)
	}

	return &ExecResult{
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		ExitCode: inspectResp.ExitCode,
	}, nil
}

// WriteFile writes content to a file inside the sandbox container.
func (s *Sandbox) WriteFile(ctx context.Context, path string, content []byte) error {
	s.LastUsedAt = time.Now()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	hdr := &tar.Header{
		Name: path,
		Mode: 0o644,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("writing tar header: %w", err)
	}
	if _, err := tw.Write(content); err != nil {
		return fmt.Errorf("writing tar content: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("closing tar writer: %w", err)
	}

	err := s.cli.CopyToContainer(ctx, s.ID, "/", &buf, container.CopyToContainerOptions{})
	if err != nil {
		return fmt.Errorf("copying to container: %w", err)
	}

	return nil
}

// ReadFile reads a file from inside the sandbox container.
func (s *Sandbox) ReadFile(ctx context.Context, path string) (string, error) {
	s.LastUsedAt = time.Now()

	reader, _, err := s.cli.CopyFromContainer(ctx, s.ID, path)
	if err != nil {
		return "", fmt.Errorf("copying from container: %w", err)
	}
	defer func() { _ = reader.Close() }()

	tr := tar.NewReader(reader)
	hdr, err := tr.Next()
	if err != nil {
		return "", fmt.Errorf("reading tar: %w", err)
	}

	if hdr.Size > 10*1024*1024 { // 10MB limit
		return "", fmt.Errorf("file too large: %d bytes", hdr.Size)
	}

	content, err := io.ReadAll(tr)
	if err != nil {
		return "", fmt.Errorf("reading file content: %w", err)
	}

	return string(content), nil
}

// parseMultiplexedOutput splits Docker's multiplexed stream into stdout and stderr.
// Docker uses an 8-byte header: [streamType(1), padding(3), size(4)].
// This is used only for unit testing the parsing logic.
func parseMultiplexedOutput(raw []byte) (string, string) {
	var stdout, stderr bytes.Buffer
	reader := bytes.NewReader(raw)

	for reader.Len() > 0 {
		header := make([]byte, 8)
		if _, err := io.ReadFull(reader, header); err != nil {
			break
		}

		streamType := header[0]
		size := int(header[4]) | int(header[5])<<8 | int(header[6])<<16 | int(header[7])<<24

		payload := make([]byte, size)
		if _, err := io.ReadFull(reader, payload); err != nil {
			break
		}

		switch streamType {
		case 1:
			stdout.Write(payload)
		case 2:
			stderr.Write(payload)
		}
	}

	return stdout.String(), stderr.String()
}
