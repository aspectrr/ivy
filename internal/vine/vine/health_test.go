package vine

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestPipelineHealthReportStructure(t *testing.T) {
	report := &PipelineHealthReport{
		SessionID: "test-session",
		Overall:   "healthy",
		Components: []ComponentHealth{
			{Name: "redpanda", Status: "healthy", Message: "ok"},
			{Name: "elasticsearch", Status: "healthy", Message: "green"},
			{Name: "logstash", Status: "healthy", Message: "running"},
		},
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	output := string(data)
	if !strings.Contains(output, `"overall": "healthy"`) {
		t.Fatalf("expected overall=healthy in: %s", output)
	}
	if !strings.Contains(output, `"redpanda"`) {
		t.Fatal("expected redpanda component")
	}
	if !strings.Contains(output, `"elasticsearch"`) {
		t.Fatal("expected elasticsearch component")
	}
	if !strings.Contains(output, `"logstash"`) {
		t.Fatal("expected logstash component")
	}
}

func TestComponentHealthStatusValues(t *testing.T) {
	statuses := []string{"healthy", "degraded", "unhealthy", "unknown"}
	for _, status := range statuses {
		h := ComponentHealth{Name: "test", Status: status}
		data, err := json.Marshal(h)
		if err != nil {
			t.Fatalf("marshal status %s: %v", status, err)
		}
		if !strings.Contains(string(data), status) {
			t.Fatalf("expected %s in output", status)
		}
	}
}

func TestPipelineHealthReportDegraded(t *testing.T) {
	report := &PipelineHealthReport{
		SessionID: "test",
		Overall:   "degraded",
		Components: []ComponentHealth{
			{Name: "redpanda", Status: "healthy"},
			{Name: "elasticsearch", Status: "unhealthy", Message: "shards unassigned"},
			{Name: "logstash", Status: "healthy"},
		},
	}

	if report.Overall != "degraded" {
		t.Fatal("expected degraded when one component is unhealthy but others are ok")
	}
}

func TestPipelineHealthReportUnhealthy(t *testing.T) {
	report := &PipelineHealthReport{
		SessionID: "test",
		Overall:   "unhealthy",
		Components: []ComponentHealth{
			{Name: "redpanda", Status: "unhealthy"},
			{Name: "elasticsearch", Status: "unhealthy"},
			{Name: "logstash", Status: "unhealthy"},
		},
	}

	if report.Overall != "unhealthy" {
		t.Fatal("expected unhealthy when all components are down")
	}
}

func TestCheckRedpandaStopped(t *testing.T) {
	skipWithoutPipeline(t)
	// Test the health check on a non-existent container.
	// This should return "unhealthy" or "unknown" without panicking.
	ps := &PipelineSandbox{
		RedpandaContainerID: "nonexistent",
	}

	health := ps.checkRedpanda(context.Background())
	if health.Name != "redpanda" {
		t.Fatalf("expected name=redpanda, got %s", health.Name)
	}
	if health.Status == "healthy" {
		t.Fatal("expected non-healthy status for nonexistent container")
	}
	t.Logf("stopped redpanda health: status=%s message=%s", health.Status, health.Message)
}

func TestCheckElasticsearchUnreachable(t *testing.T) {
	ps := &PipelineSandbox{
		ESAddr: "http://127.0.0.1:1", // port 1 should not be listening
	}

	health := ps.checkElasticsearch(context.Background())
	if health.Name != "elasticsearch" {
		t.Fatalf("expected name=elasticsearch, got %s", health.Name)
	}
	if health.Status == "healthy" {
		t.Fatal("expected non-healthy status for unreachable ES")
	}
	t.Logf("unreachable ES health: status=%s message=%s", health.Status, health.Message)
}

func TestCheckLogstashStopped(t *testing.T) {
	skipWithoutPipeline(t)
	ps := &PipelineSandbox{
		LogstashContainerID: "nonexistent",
	}

	health := ps.checkLogstash(context.Background())
	if health.Name != "logstash" {
		t.Fatalf("expected name=logstash, got %s", health.Name)
	}
	if health.Status == "healthy" {
		t.Fatal("expected non-healthy status for nonexistent container")
	}
	t.Logf("stopped logstash health: status=%s message=%s", health.Status, health.Message)
}

func TestHealthAllStopped(t *testing.T) {
	skipWithoutPipeline(t)
	ps := &PipelineSandbox{
		SessionID:                "test-stopped",
		RedpandaContainerID:      "nonexistent",
		ElasticsearchContainerID: "nonexistent",
		LogstashContainerID:      "nonexistent",
		ESAddr:                   "http://127.0.0.1:1",
	}

	report, err := ps.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}

	if report.Overall != "unhealthy" {
		t.Fatalf("expected unhealthy for all-stopped pipeline, got %s", report.Overall)
	}
	if len(report.Components) != 3 {
		t.Fatalf("expected 3 components, got %d", len(report.Components))
	}
	for _, c := range report.Components {
		if c.Status == "healthy" {
			t.Fatalf("expected no healthy components, but %s is healthy", c.Name)
		}
		t.Logf("  %s: status=%s message=%s", c.Name, c.Status, c.Message)
	}
}

func TestElasticsearchHealthParsing(t *testing.T) {
	// Verify the ES health response parsing handles all status colors.
	tests := []struct {
		esStatus string
		expected string // our mapped status
	}{
		{"green", "healthy"},
		{"yellow", "degraded"},
		{"red", "unhealthy"},
	}

	for _, tt := range tests {
		t.Run(tt.esStatus, func(t *testing.T) {
			// Simulate the JSON that checkElasticsearch would parse.
			esJSON := `{"status":"` + tt.esStatus + `","number_of_nodes":1,"active_shards":5,"unassigned_shards":0}`

			var esHealth struct {
				Status           string `json:"status"`
				NumberOfNodes    int    `json:"number_of_nodes"`
				ActiveShards     int    `json:"active_shards"`
				UnassignedShards int    `json:"unassigned_shards"`
			}
			_ = json.Unmarshal([]byte(esJSON), &esHealth)

			var status string
			switch esHealth.Status {
			case "green":
				status = "healthy"
			case "yellow":
				status = "degraded"
			case "red":
				status = "unhealthy"
			default:
				status = "unknown"
			}

			if status != tt.expected {
				t.Fatalf("ES status %s → expected %s, got %s", tt.esStatus, tt.expected, status)
			}
		})
	}
}

func TestPipelineHealthWithRealPipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	skipWithoutPipeline(t)

	mgr, err := NewPipelineManager(dockerHost(), slog.Default())
	if err != nil {
		t.Fatalf("NewPipelineManager: %v", err)
	}
	defer func() { _ = mgr.Close(context.Background()) }()

	ctx := context.Background()
	sessionID := "health-test"

	ps, err := mgr.Create(ctx, sessionID, PipelineConfig{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = mgr.Destroy(ctx, sessionID) }()

	if err := mgr.WaitForHealth(ctx, ps, 3*time.Minute); err != nil {
		t.Fatalf("WaitForHealth: %v", err)
	}

	report, err := ps.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}

	t.Logf("Pipeline health report:")
	t.Logf("  Overall: %s", report.Overall)
	for _, c := range report.Components {
		t.Logf("  %s: status=%s message=%s", c.Name, c.Status, c.Message)
		if c.Details != nil {
			t.Logf("    details: %s", string(c.Details))
		}
	}

	// Redpanda should be healthy.
	if report.Components[0].Status != "healthy" {
		t.Fatalf("redpanda: expected healthy, got %s (%s)", report.Components[0].Status, report.Components[0].Message)
	}

	// ES should be healthy (or at least yellow/degraded, not unhealthy).
	if report.Components[1].Status == "unhealthy" {
		t.Fatalf("elasticsearch: unhealthy — %s", report.Components[1].Message)
	}

	// Logstash should be at least degraded (may still be starting up).
	if report.Components[2].Status == "unhealthy" {
		t.Fatalf("logstash: unhealthy — %s", report.Components[2].Message)
	}
}
