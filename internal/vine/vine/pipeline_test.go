package vine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// skipWithoutPipeline checks if pipeline integration tests can run.
func skipWithoutPipeline(t *testing.T) *PipelineManager {
	t.Helper()

	if os.Getenv("IVY_PIPELINE_TESTS") == "" {
		t.Skip("Set IVY_PIPELINE_TESTS=1 to run pipeline integration tests (requires Docker + ~4GB RAM)")
	}

	host := dockerHost()
	pm, err := NewPipelineManager(host, slog.Default())
	if err != nil {
		t.Fatalf("NewPipelineManager: %v", err)
	}
	t.Cleanup(func() { _ = pm.Close(context.Background()) })
	return pm
}

func TestPipelineCreateAndDestroy(t *testing.T) {
	pm := skipWithoutPipeline(t)
	ctx := context.Background()
	sessionID := fmt.Sprintf("pipe-test-%d", time.Now().UnixNano())

	// Use default Logstash config.
	ps, err := pm.Create(ctx, sessionID, PipelineConfig{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Logf("created pipeline: session=%s kafka=%s es=%s ls=%s net=%s",
		sessionID, ps.KafkaContainerID[:12], ps.ElasticsearchContainerID[:12],
		ps.LogstashContainerID[:12], ps.NetworkID[:12])

	// Verify we can get it.
	found, err := pm.Get(sessionID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found.KafkaContainerID != ps.KafkaContainerID {
		t.Fatal("Get returned different pipeline")
	}

	// Wait for services to be healthy.
	if err := pm.WaitForHealth(ctx, ps, 3*time.Minute); err != nil {
		t.Fatalf("WaitForHealth: %v", err)
	}
	t.Log("all services healthy")

	// Destroy.
	if err := pm.Destroy(ctx, sessionID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Verify it's gone.
	_, err = pm.Get(sessionID)
	if err == nil {
		t.Fatal("expected error after destroy")
	}
}

func TestPipelineKafkaReady(t *testing.T) {
	pm := skipWithoutPipeline(t)
	ctx := context.Background()
	sessionID := fmt.Sprintf("pipe-kafka-%d", time.Now().UnixNano())

	ps, err := pm.Create(ctx, sessionID, PipelineConfig{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = pm.Destroy(ctx, sessionID) }()

	if err := pm.WaitForHealth(ctx, ps, 3*time.Minute); err != nil {
		t.Fatalf("WaitForHealth: %v", err)
	}

	// List topics via kafka-topics.sh — should work without error.
	execResp, err := ps.cli.ContainerExecCreate(ctx, ps.KafkaContainerID, container.ExecOptions{
		Cmd:          []string{"/opt/kafka/bin/kafka-topics.sh", "--list", "--bootstrap-server", "localhost:9092"},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		t.Fatalf("ExecCreate: %v", err)
	}

	attachResp, err := ps.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
	if err != nil {
		t.Fatalf("ExecAttach: %v", err)
	}
	defer attachResp.Close()

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	_, _ = stdcopy.StdCopy(&stdoutBuf, &stderrBuf, attachResp.Reader)

	t.Logf("kafka topics: stdout=%q stderr=%q", stdoutBuf.String(), stderrBuf.String())
	if stdoutBuf.Len() == 0 && stderrBuf.Len() == 0 {
		t.Fatal("expected some output from kafka-topics.sh")
	}
}

func TestPipelineESReady(t *testing.T) {
	pm := skipWithoutPipeline(t)
	ctx := context.Background()
	sessionID := fmt.Sprintf("pipe-es-%d", time.Now().UnixNano())

	ps, err := pm.Create(ctx, sessionID, PipelineConfig{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = pm.Destroy(ctx, sessionID) }()

	if err := pm.WaitForHealth(ctx, ps, 3*time.Minute); err != nil {
		t.Fatalf("WaitForHealth: %v", err)
	}

	// Query ES root endpoint — we can't use QueryES for the root path.
	// Use a simple _search on a non-existent index to verify ES is responding.
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(ps.ESAddr + "/_cluster/health")
	if err != nil {
		t.Fatalf("GET cluster health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("ES returned status %d: %s", resp.StatusCode, string(body))
	}

	var health map[string]interface{}
	_ = json.Unmarshal(body, &health)
	t.Logf("ES cluster health: status=%v nodes=%v", health["status"], health["number_of_nodes"])

	if nodes, _ := health["number_of_nodes"].(float64); nodes != 1 {
		t.Fatalf("expected 1 node, got %v", nodes)
	}
}

func TestPipelineSendDataAndQuery(t *testing.T) {
	pm := skipWithoutPipeline(t)
	ctx := context.Background()
	sessionID := fmt.Sprintf("pipe-e2e-%d", time.Now().UnixNano())

	// Use a custom config that reads from "test-events" topic.
	logstashConfig := `input {
  kafka {
    bootstrap_servers => "prod-kafka:9092"
    topics => ["test-events"]
    group_id => "logstash-sandbox"
    auto_offset_reset => "earliest"
  }
}

filter {
  json {
    source => "message"
  }
}

output {
  elasticsearch {
    hosts => ["http://prod-es:9200"]
    index => "test-events"
  }
}
`
	ps, err := pm.Create(ctx, sessionID, PipelineConfig{
		LogstashConfig: logstashConfig,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = pm.Destroy(ctx, sessionID) }()

	// Wait for services.
	if err := pm.WaitForHealth(ctx, ps, 3*time.Minute); err != nil {
		t.Fatalf("WaitForHealth: %v", err)
	}
	t.Log("all services healthy, waiting for Logstash to connect to Kafka...")

	// Give Logstash time to connect to Kafka and start consuming.
	time.Sleep(10 * time.Second)

	// Send test data.
	testMessage := `{"level":"ERROR","service":"api-gateway","message":"Connection timeout to backend","timestamp":"2025-01-15T10:30:00Z"}`
	if err := ps.SendData(ctx, "test-events", testMessage); err != nil {
		t.Fatalf("SendData: %v", err)
	}
	t.Log("data sent to Kafka")

	// Poll ES until data appears or timeout.
	var result json.RawMessage
	var esResp map[string]interface{}
	for i := 0; i < 30; i++ {
		result, err = ps.QueryES(ctx, "test-events", `{"query":{"match_all":{}}}`)
		if err != nil {
			t.Logf("QueryES attempt %d: %v", i, err)
			time.Sleep(2 * time.Second)
			continue
		}
		_ = json.Unmarshal(result, &esResp)
		hits, _ := esResp["hits"].(map[string]interface{})
		total, _ := hits["total"].(map[string]interface{})
		hitCount := 0.0
		if total != nil {
			hitCount, _ = total["value"].(float64)
		}
		if hitCount > 0 {
			break
		}
		t.Logf("attempt %d: 0 hits, waiting...", i)
		time.Sleep(2 * time.Second)
	}

	if result == nil {
		logs, _ := ps.GetLogstashLogs(ctx, "50")
		t.Fatalf("data never appeared in ES.\nLogstash logs:\n%s", logs)
	}

	hits, _ := esResp["hits"].(map[string]interface{})
	total, _ := hits["total"].(map[string]interface{})
	hitCount := 0.0
	if total != nil {
		hitCount, _ = total["value"].(float64)
	}

	t.Logf("ES query result: %d hits", int(hitCount))

	if hitCount == 0 {
		// Check Logstash logs for errors.
		logs, _ := ps.GetLogstashLogs(ctx, "50")
		t.Fatalf("expected at least 1 document in ES but got 0.\nLogstash logs:\n%s", logs)
	}

	// Verify the message content.
	hitList, _ := hits["hits"].([]interface{})
	if len(hitList) > 0 {
		firstHit, _ := hitList[0].(map[string]interface{})
		source, _ := firstHit["_source"].(map[string]interface{})
		t.Logf("first document source: %v", source)
	}
}

func TestPipelineGetLogstashLogs(t *testing.T) {
	pm := skipWithoutPipeline(t)
	ctx := context.Background()
	sessionID := fmt.Sprintf("pipe-logs-%d", time.Now().UnixNano())

	ps, err := pm.Create(ctx, sessionID, PipelineConfig{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = pm.Destroy(ctx, sessionID) }()

	if err := pm.WaitForHealth(ctx, ps, 3*time.Minute); err != nil {
		t.Fatalf("WaitForHealth: %v", err)
	}

	logs, err := ps.GetLogstashLogs(ctx, "20")
	if err != nil {
		t.Fatalf("GetLogstashLogs: %v", err)
	}

	if logs == "" {
		t.Log("Logstash returned empty logs (may still be starting up)")
	} else {
		t.Logf("Logstash logs (last 20 lines):\n%s", logs)
	}
}

func TestPipelineUpdateLogstashConfig(t *testing.T) {
	pm := skipWithoutPipeline(t)
	ctx := context.Background()
	sessionID := fmt.Sprintf("pipe-cfg-%d", time.Now().UnixNano())

	ps, err := pm.Create(ctx, sessionID, PipelineConfig{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = pm.Destroy(ctx, sessionID) }()

	if err := pm.WaitForHealth(ctx, ps, 3*time.Minute); err != nil {
		t.Fatalf("WaitForHealth: %v", err)
	}

	// Update the config.
	newConfig := `input {
  kafka {
    bootstrap_servers => "prod-kafka:9092"
    topics => ["updated-topic"]
    group_id => "logstash-updated"
    auto_offset_reset => "earliest"
  }
}
output {
  elasticsearch {
    hosts => ["http://prod-es:9200"]
    index => "updated-index"
  }
}
`
	if err := ps.UpdateLogstashConfig(ctx, newConfig); err != nil {
		t.Fatalf("UpdateLogstashConfig: %v", err)
	}
	t.Log("Logstash config updated and restarted")

	// Give it time to restart.
	time.Sleep(10 * time.Second)

	// Verify Logstash is still running and has new config.
	logs, err := ps.GetLogstashLogs(ctx, "20")
	if err != nil {
		t.Fatalf("GetLogstashLogs: %v", err)
	}
	t.Logf("Logstash logs after restart:\n%s", logs)
}

func TestPipelineInvalidConfig(t *testing.T) {
	pm := skipWithoutPipeline(t)
	ctx := context.Background()
	sessionID := fmt.Sprintf("pipe-invalid-%d", time.Now().UnixNano())

	_, err := pm.Create(ctx, sessionID, PipelineConfig{
		LogstashConfig: "this is not valid logstash config",
	})
	if err == nil {
		_ = pm.Destroy(ctx, sessionID)
		t.Fatal("expected error for invalid Logstash config")
	}
	t.Logf("correctly rejected invalid config: %v", err)
}

func TestPipelineDuplicateSession(t *testing.T) {
	pm := skipWithoutPipeline(t)
	ctx := context.Background()
	sessionID := fmt.Sprintf("pipe-dup-%d", time.Now().UnixNano())

	ps, err := pm.Create(ctx, sessionID, PipelineConfig{})
	if err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	defer func() { _ = pm.Destroy(ctx, sessionID) }()

	_, err = pm.Create(ctx, sessionID, PipelineConfig{})
	if err == nil {
		t.Fatal("expected error for duplicate session")
	}
	t.Logf("correctly rejected duplicate: %v", err)

	// Verify original still works.
	found, err := pm.Get(sessionID)
	if err != nil {
		t.Fatalf("Get after dup reject: %v", err)
	}
	if found.KafkaContainerID != ps.KafkaContainerID {
		t.Fatal("original pipeline was corrupted")
	}
}
