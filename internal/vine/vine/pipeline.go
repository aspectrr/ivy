package vine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

// PipelineSandbox represents a running Redpanda → Logstash → ES pipeline.
type PipelineSandbox struct {
	SessionID string
	NetworkID string

	RedpandaContainerID      string
	ElasticsearchContainerID string
	LogstashContainerID      string

	// KafkaContainerID is kept for backward compat; points to the Redpanda container.
	KafkaContainerID string

	KafkaAddr string // host:port for producing messages (Kafka protocol compatible)
	ESAddr    string // http://host:port for querying

	CreatedAt  time.Time
	LastUsedAt time.Time

	cli *client.Client
}

// PipelineConfig holds configuration for creating a pipeline sandbox.
type PipelineConfig struct {
	RedpandaImage      string
	ElasticsearchImage string
	LogstashImage      string
	LogstashConfig     string // Raw Logstash pipeline config
}

// PipelineManager manages the lifecycle of pipeline sandboxes.
// Each pipeline sandbox gets its own Docker network and 3 containers:
// Redpanda (Kafka-compatible), Logstash, Elasticsearch.
type PipelineManager struct {
	cli       *client.Client
	logger    *slog.Logger
	pipelines map[string]*PipelineSandbox // sessionID → PipelineSandbox
}

// NewPipelineManager creates a new pipeline sandbox manager.
func NewPipelineManager(dockerHost string, logger *slog.Logger) (*PipelineManager, error) {
	opts := []client.Opt{client.FromEnv}
	if dockerHost != "" {
		opts = append(opts, client.WithHost(dockerHost))
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("creating Docker client: %w", err)
	}

	return &PipelineManager{
		cli:       cli,
		logger:    logger,
		pipelines: make(map[string]*PipelineSandbox),
	}, nil
}

// Create spins up a full Redpanda → Logstash → ES pipeline for a session.
func (pm *PipelineManager) Create(ctx context.Context, sessionID string, cfg PipelineConfig) (_ *PipelineSandbox, retErr error) {
	var ps *PipelineSandbox
	if _, exists := pm.pipelines[sessionID]; exists {
		return nil, fmt.Errorf("pipeline already exists for session %s", sessionID)
	}

	// Apply defaults.
	if cfg.RedpandaImage == "" {
		cfg.RedpandaImage = "docker.redpanda.com/redpandadata/redpanda:latest"
	}
	if cfg.ElasticsearchImage == "" {
		cfg.ElasticsearchImage = "docker.elastic.co/elasticsearch/elasticsearch:8.17.0"
	}
	if cfg.LogstashImage == "" {
		cfg.LogstashImage = "docker.elastic.co/logstash/logstash:8.17.0"
	}

	// Validate and rewrite Logstash config.
	if strings.TrimSpace(cfg.LogstashConfig) == "" {
		cfg.LogstashConfig = defaultLogstashConfig()
	} else {
		if err := ValidateLogstashConfig(cfg.LogstashConfig); err != nil {
			return nil, fmt.Errorf("invalid Logstash config: %w", err)
		}
	}
	rewrittenConfig := RewriteLogstashConfig(cfg.LogstashConfig)

	prefix := fmt.Sprintf("ivy-pipe-%s", sessionID)
	if len(prefix) > 40 {
		prefix = prefix[:40]
	}

	// 1. Create dedicated Docker network.
	netResp, err := pm.cli.NetworkCreate(ctx, prefix, network.CreateOptions{
		Labels: map[string]string{
			"ivy-session-id": sessionID,
			"ivy-type":       "pipeline-network",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("creating network: %w", err)
	}
	networkID := netResp.ID

	// Cleanup helper: remove network and any containers created so far.
	cleanup := func(rpID, esID, lsID string) {
		if rpID != "" {
			_ = pm.cli.ContainerRemove(ctx, rpID, container.RemoveOptions{Force: true})
		}
		if esID != "" {
			_ = pm.cli.ContainerRemove(ctx, esID, container.RemoveOptions{Force: true})
		}
		if lsID != "" {
			_ = pm.cli.ContainerRemove(ctx, lsID, container.RemoveOptions{Force: true})
		}
		_ = pm.cli.NetworkRemove(ctx, networkID)
	}

	// 2. Start Redpanda.
	rpID, err := pm.startRedpanda(ctx, prefix+"-redpanda", networkID, cfg.RedpandaImage)
	if err != nil {
		cleanup("", "", "")
		return nil, fmt.Errorf("starting Redpanda: %w", err)
	}

	// 3. Start Elasticsearch.
	esID, err := pm.startElasticsearch(ctx, prefix+"-es", networkID, cfg.ElasticsearchImage)
	if err != nil {
		cleanup(rpID, "", "")
		return nil, fmt.Errorf("starting Elasticsearch: %w", err)
	}

	// 4. Wait for ES to be healthy before starting Logstash.
	// This prevents Logstash from marking ES as dead on first connect.
	ps = &PipelineSandbox{
		ElasticsearchContainerID: esID,
		cli:                      pm.cli,
	}
	ps.ESAddr, err = pm.getESHostAddr(ctx, esID)
	if err != nil {
		cleanup(rpID, esID, "")
		return nil, fmt.Errorf("getting ES address: %w", err)
	}

	pm.logger.Info("waiting for ES before starting Logstash")
	if err := pm.waitForES(ctx, ps, time.Now().Add(3*time.Minute)); err != nil {
		cleanup(rpID, esID, "")
		return nil, fmt.Errorf("waiting for ES: %w", err)
	}

	// 5. Start Logstash with rewritten config (ES is now ready).
	lsID, err := pm.startLogstash(ctx, prefix+"-logstash", networkID, cfg.LogstashImage, rewrittenConfig)
	if err != nil {
		cleanup(rpID, esID, "")
		return nil, fmt.Errorf("starting Logstash: %w", err)
	}

	now := time.Now()
	ps = &PipelineSandbox{
		SessionID:                sessionID,
		NetworkID:                networkID,
		RedpandaContainerID:      rpID,
		KafkaContainerID:         rpID, // backward compat
		ElasticsearchContainerID: esID,
		LogstashContainerID:      lsID,
		KafkaAddr:                "redpanda:9092", // internal alias
		ESAddr:                   ps.ESAddr,
		CreatedAt:                now,
		LastUsedAt:               now,
		cli:                      pm.cli,
	}

	pm.pipelines[sessionID] = ps

	pm.logger.Info("pipeline sandbox created",
		"session_id", sessionID,
		"redpanda_id", rpID,
		"es_id", esID,
		"logstash_id", lsID,
		"network_id", networkID,
	)

	return ps, nil
}

// Get retrieves a pipeline sandbox by session ID.
func (pm *PipelineManager) Get(sessionID string) (*PipelineSandbox, error) {
	ps, ok := pm.pipelines[sessionID]
	if !ok {
		return nil, fmt.Errorf("no pipeline sandbox for session %s", sessionID)
	}
	return ps, nil
}

// Destroy tears down the pipeline sandbox: stops and removes all containers and the network.
func (pm *PipelineManager) Destroy(ctx context.Context, sessionID string) error {
	ps, ok := pm.pipelines[sessionID]
	if !ok {
		return fmt.Errorf("no pipeline sandbox for session %s", sessionID)
	}

	removeOpts := container.RemoveOptions{Force: true}

	// Remove containers in reverse order: Logstash → ES → Redpanda.
	if ps.LogstashContainerID != "" {
		_ = pm.cli.ContainerRemove(ctx, ps.LogstashContainerID, removeOpts)
	}
	if ps.ElasticsearchContainerID != "" {
		_ = pm.cli.ContainerRemove(ctx, ps.ElasticsearchContainerID, removeOpts)
	}
	if ps.RedpandaContainerID != "" {
		_ = pm.cli.ContainerRemove(ctx, ps.RedpandaContainerID, removeOpts)
	}

	// Remove the dedicated network.
	if ps.NetworkID != "" {
		_ = pm.cli.NetworkRemove(ctx, ps.NetworkID)
	}

	delete(pm.pipelines, sessionID)

	pm.logger.Info("pipeline sandbox destroyed", "session_id", sessionID)
	return nil
}

// Close tears down all pipeline sandboxes.
func (pm *PipelineManager) Close(ctx context.Context) error {
	for sessionID := range pm.pipelines {
		_ = pm.Destroy(ctx, sessionID)
	}
	return pm.cli.Close()
}

// SendData produces a message to the pipeline's Redpanda broker using rpk.
func (ps *PipelineSandbox) SendData(ctx context.Context, topic, data string) error {
	ps.LastUsedAt = time.Now()

	// rpk topic produce handles topic auto-creation in dev-container mode.
	produceCmd := []string{
		"/bin/bash", "-c",
		fmt.Sprintf("echo '%s' | rpk topic produce %s --format '%%v'",
			escapeForShell(data), topic,
		),
	}

	execResp, err := ps.cli.ContainerExecCreate(ctx, ps.RedpandaContainerID, container.ExecOptions{
		Cmd:          produceCmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return fmt.Errorf("creating producer exec: %w", err)
	}

	attachResp, err := ps.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
	if err != nil {
		return fmt.Errorf("attaching producer exec: %w", err)
	}
	defer attachResp.Close()

	// Drain output.
	_, _ = io.ReadAll(attachResp.Reader)

	inspectResp, err := ps.cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return fmt.Errorf("inspecting producer exec: %w", err)
	}
	if inspectResp.ExitCode != 0 {
		return fmt.Errorf("rpk topic produce exited with code %d", inspectResp.ExitCode)
	}

	return nil
}

// QueryES queries the pipeline's Elasticsearch instance.
// query can be a simple text search string or a JSON ES query DSL body.
func (ps *PipelineSandbox) QueryES(ctx context.Context, index string, query string) (json.RawMessage, error) {
	ps.LastUsedAt = time.Now()

	url := fmt.Sprintf("%s/%s/_search", ps.ESAddr, index)

	// Determine if query is JSON (DSL) or plain text.
	var body string
	if json.Valid([]byte(query)) {
		body = query
	} else {
		// Simple text search via query_string.
		b, _ := json.Marshal(map[string]interface{}{
			"query": map[string]interface{}{
				"query_string": map[string]interface{}{
					"query": "*" + query + "*",
				},
			},
		})
		body = string(b)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating ES request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying ES: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	result, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading ES response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("ES returned status %d: %s", resp.StatusCode, string(result))
	}

	return json.RawMessage(result), nil
}

// GetLogstashLogs retrieves recent Logstash container logs.
func (ps *PipelineSandbox) GetLogstashLogs(ctx context.Context, tail string) (string, error) {
	ps.LastUsedAt = time.Now()

	if tail == "" {
		tail = "100"
	}

	logs, err := ps.cli.ContainerLogs(ctx, ps.LogstashContainerID, container.LogsOptions{
		Tail:       tail,
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		return "", fmt.Errorf("getting Logstash logs: %w", err)
	}
	defer func() { _ = logs.Close() }()

	data, err := io.ReadAll(logs)
	if err != nil {
		return "", fmt.Errorf("reading Logstash logs: %w", err)
	}

	return stripDockerLogHeaders(data), nil
}

// UpdateLogstashConfig replaces the Logstash pipeline config and restarts the container.
func (ps *PipelineSandbox) UpdateLogstashConfig(ctx context.Context, config string) error {
	ps.LastUsedAt = time.Now()

	if err := ValidateLogstashConfig(config); err != nil {
		return fmt.Errorf("invalid Logstash config: %w", err)
	}

	rewritten := RewriteLogstashConfig(config)

	if err := ps.writeLogstashConfig(ctx, rewritten); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	if err := ps.cli.ContainerRestart(ctx, ps.LogstashContainerID, container.StopOptions{}); err != nil {
		return fmt.Errorf("restarting Logstash: %w", err)
	}

	return nil
}

// --- Internal helpers ---

func (pm *PipelineManager) startRedpanda(ctx context.Context, name, networkID, image string) (string, error) {
	createResp, err := pm.cli.ContainerCreate(ctx,
		&container.Config{
			Image: image,
			Cmd: []string{
				"redpanda", "start",
				"--mode", "dev-container",
				"--node-id", "0",
				"--kafka-addr", "PLAINTEXT://0.0.0.0:9092",
				"--advertise-kafka-addr", "PLAINTEXT://redpanda:9092",
				"--smp", "1",
				"--memory", "512M",
			},
			Labels: map[string]string{
				"ivy-type":       "pipeline-redpanda",
				"ivy-session-id": name,
			},
		},
		&container.HostConfig{},
		nil, nil, name,
	)
	if err != nil {
		return "", fmt.Errorf("creating Redpanda container: %w", err)
	}

	if err := pm.cli.NetworkConnect(ctx, networkID, createResp.ID, &network.EndpointSettings{
		Aliases: []string{"redpanda", "kafka"}, // "kafka" alias for Logstash compat
	}); err != nil {
		_ = pm.cli.ContainerRemove(ctx, createResp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("connecting Redpanda to network: %w", err)
	}

	if err := pm.cli.ContainerStart(ctx, createResp.ID, container.StartOptions{}); err != nil {
		_ = pm.cli.ContainerRemove(ctx, createResp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("starting Redpanda container: %w", err)
	}

	return createResp.ID, nil
}

func (pm *PipelineManager) startElasticsearch(ctx context.Context, name, networkID, image string) (string, error) {
	createResp, err := pm.cli.ContainerCreate(ctx,
		&container.Config{
			Image: image,
			Env: []string{
				"discovery.type=single-node",
				"xpack.security.enabled=false",
				"xpack.security.http.ssl.enabled=false",
				"ES_JAVA_OPTS=-Xms512m -Xmx512m",
				"cluster.routing.allocation.disk.threshold_enabled=false",
			},
			Labels: map[string]string{
				"ivy-type":       "pipeline-es",
				"ivy-session-id": name,
			},
			ExposedPorts: nat.PortSet{
				"9200/tcp": struct{}{},
			},
		},
		&container.HostConfig{
			PortBindings: nat.PortMap{
				"9200/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "0"}},
			},
		},
		nil, nil, name,
	)
	if err != nil {
		return "", fmt.Errorf("creating ES container: %w", err)
	}

	if err := pm.cli.NetworkConnect(ctx, networkID, createResp.ID, &network.EndpointSettings{
		Aliases: []string{"elasticsearch"},
	}); err != nil {
		_ = pm.cli.ContainerRemove(ctx, createResp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("connecting ES to network: %w", err)
	}

	if err := pm.cli.ContainerStart(ctx, createResp.ID, container.StartOptions{}); err != nil {
		_ = pm.cli.ContainerRemove(ctx, createResp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("starting ES container: %w", err)
	}

	return createResp.ID, nil
}

func (pm *PipelineManager) startLogstash(ctx context.Context, name, networkID, image, config string) (string, error) {
	createResp, err := pm.cli.ContainerCreate(ctx,
		&container.Config{
			Image: image,
			Env: []string{
				"LS_JAVA_OPTS=-Xms256m -Xmx256m",
			},
			Labels: map[string]string{
				"ivy-type":       "pipeline-logstash",
				"ivy-session-id": name,
			},
		},
		&container.HostConfig{},
		nil, nil, name,
	)
	if err != nil {
		return "", fmt.Errorf("creating Logstash container: %w", err)
	}

	if err := pm.cli.NetworkConnect(ctx, networkID, createResp.ID, &network.EndpointSettings{
		Aliases: []string{"logstash"},
	}); err != nil {
		_ = pm.cli.ContainerRemove(ctx, createResp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("connecting Logstash to network: %w", err)
	}

	// Write the pipeline config BEFORE starting the container.
	ls := &PipelineSandbox{LogstashContainerID: createResp.ID, cli: pm.cli}
	if err := ls.writeLogstashConfig(ctx, config); err != nil {
		_ = pm.cli.ContainerRemove(ctx, createResp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("writing Logstash config: %w", err)
	}

	// Now start — Logstash will pick up the config on first boot.
	if err := pm.cli.ContainerStart(ctx, createResp.ID, container.StartOptions{}); err != nil {
		_ = pm.cli.ContainerRemove(ctx, createResp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("starting Logstash container: %w", err)
	}

	return createResp.ID, nil
}

func (pm *PipelineManager) getESHostAddr(ctx context.Context, containerID string) (string, error) {
	inspect, err := pm.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", err
	}
	if inspect.NetworkSettings != nil && inspect.NetworkSettings.Ports != nil {
		bindings := inspect.NetworkSettings.Ports["9200/tcp"]
		if len(bindings) > 0 {
			return fmt.Sprintf("http://127.0.0.1:%s", bindings[0].HostPort), nil
		}
	}
	return "", fmt.Errorf("ES port 9200 not bound for container %s", containerID)
}

func (ps *PipelineSandbox) writeLogstashConfig(ctx context.Context, config string) error {
	sb := &Sandbox{ID: ps.LogstashContainerID, cli: ps.cli}
	return sb.WriteFile(ctx, "/usr/share/logstash/pipeline/logstash.conf", []byte(config))
}

// escapeForShell escapes a string for safe embedding in a single-quoted shell string.
func escapeForShell(s string) string {
	return strings.ReplaceAll(s, "'", "'\\''")
}

// stripDockerLogHeaders removes the 8-byte Docker log framing headers.
func stripDockerLogHeaders(data []byte) string {
	var result bytes.Buffer
	reader := bytes.NewReader(data)
	for reader.Len() > 0 {
		header := make([]byte, 8)
		if _, err := io.ReadFull(reader, header); err != nil {
			break
		}
		size := int(header[4]) | int(header[5])<<8 | int(header[6])<<16 | int(header[7])<<24
		payload := make([]byte, size)
		if _, err := io.ReadFull(reader, payload); err != nil {
			break
		}
		result.Write(payload)
	}
	return result.String()
}

// defaultLogstashConfig returns a simple passthrough config for testing.
// Uses "kafka" alias which resolves to the Redpanda container via network alias.
func defaultLogstashConfig() string {
	return `input {
  kafka {
    bootstrap_servers => "kafka:9092"
    topics => ["test"]
    group_id => "logstash"
  }
}

filter {
  mutate {
    add_field => { "[@metadata][processed]" => "true" }
  }
}

output {
  elasticsearch {
    hosts => ["http://elasticsearch:9200"]
    index => "logstash-%{+YYYY.MM.dd}"
  }
}
`
}

// WaitForHealth waits for Redpanda and ES to become healthy within the timeout.
func (pm *PipelineManager) WaitForHealth(ctx context.Context, ps *PipelineSandbox, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	// Wait for Redpanda to be ready.
	if err := pm.waitForRedpanda(ctx, ps.RedpandaContainerID, deadline); err != nil {
		return err
	}

	// Wait for Elasticsearch.
	if err := pm.waitForES(ctx, ps, deadline); err != nil {
		return err
	}

	return nil
}

func (pm *PipelineManager) waitForRedpanda(ctx context.Context, containerID string, deadline time.Time) error {
	// Wait for container to be running.
	if err := pm.waitForContainer(ctx, containerID, "Redpanda", deadline); err != nil {
		return err
	}

	// Wait for Redpanda to accept Kafka API connections via rpk topic list.
	for time.Now().Before(deadline) {
		execResp, err := pm.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
			Cmd:          []string{"rpk", "topic", "list"},
			AttachStdout: true,
			AttachStderr: true,
		})
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		attachResp, err := pm.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		attachResp.Close()

		inspect, err := pm.cli.ContainerExecInspect(ctx, execResp.ID)
		if err == nil && inspect.ExitCode == 0 {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("redpanda did not become ready within timeout (container: %s)", containerID[:12])
}

func (pm *PipelineManager) waitForContainer(ctx context.Context, containerID, name string, deadline time.Time) error {
	for time.Now().Before(deadline) {
		inspect, err := pm.cli.ContainerInspect(ctx, containerID)
		if err != nil {
			return fmt.Errorf("inspecting %s: %w", name, err)
		}
		if inspect.State != nil && inspect.State.Running {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("%s did not become healthy within timeout", name)
}

func (pm *PipelineManager) waitForES(ctx context.Context, ps *PipelineSandbox, deadline time.Time) error {
	for i := 0; time.Now().Before(deadline); i++ {
		req, err := http.NewRequestWithContext(ctx, "GET", ps.ESAddr+"/_cluster/health", nil)
		if err != nil {
			return err
		}
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
			pm.logger.Info("ES not ready", "status", resp.StatusCode, "addr", ps.ESAddr)
		} else if i%10 == 0 {
			pm.logger.Info("ES not reachable", "addr", ps.ESAddr, "error", err)
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("elasticsearch did not become healthy within timeout (addr=%s)", ps.ESAddr) //nolint:staticcheck
}
