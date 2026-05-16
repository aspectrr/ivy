package tools

import (
	"context"
	"encoding/json"

	"testing"
)

// Pipeline tools are tested via the nil-provider error paths and tool definition tests.
// Full integration tests (with real Docker pipeline) are in integration_test.go.

func TestPipelineToolsRegistered(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterPipelineTools(reg, nil); err != nil {
		t.Fatalf("RegisterPipelineTools: %v", err)
	}

	expectedTools := []string{
		"pipeline_send_data",
		"pipeline_query_es",
		"pipeline_get_logstash_status",
		"pipeline_update_config",
	}

	for _, name := range expectedTools {
		tool, err := reg.Get(name)
		if err != nil {
			t.Fatalf("tool %s not registered: %v", name, err)
		}
		def := tool.Definition()
		if def.Description == "" {
			t.Fatalf("tool %s has empty description", name)
		}
		if len(def.Parameters) == 0 {
			t.Fatalf("tool %s has empty parameters", name)
		}
		t.Logf("✓ %s: %s", name, def.Description)
	}
}

func TestPipelineSendDataNoProvider(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterPipelineTools(reg, nil)

	_, err := reg.Execute(context.Background(), "pipeline_send_data",
		json.RawMessage(`{"topic":"test","data":"hello"}`),
		ToolContext{SessionID: "test-session"},
	)
	if err == nil {
		t.Fatal("expected error with nil provider")
	}
}

func TestPipelineQueryESNoProvider(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterPipelineTools(reg, nil)

	_, err := reg.Execute(context.Background(), "pipeline_query_es",
		json.RawMessage(`{"index":"test"}`),
		ToolContext{SessionID: "test-session"},
	)
	if err == nil {
		t.Fatal("expected error with nil provider")
	}
}

func TestPipelineGetLogstashStatusNoProvider(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterPipelineTools(reg, nil)

	_, err := reg.Execute(context.Background(), "pipeline_get_logstash_status",
		json.RawMessage(`{}`),
		ToolContext{SessionID: "test-session"},
	)
	if err == nil {
		t.Fatal("expected error with nil provider")
	}
}

func TestPipelineUpdateConfigNoProvider(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterPipelineTools(reg, nil)

	_, err := reg.Execute(context.Background(), "pipeline_update_config",
		json.RawMessage(`{"config":"input { kafka {} } output { elasticsearch {} }"}`),
		ToolContext{SessionID: "test-session"},
	)
	if err == nil {
		t.Fatal("expected error with nil provider")
	}
}

func TestPipelineSendDataValidation(t *testing.T) {
	tool := &PipelineSendDataTool{Provider: nil}
	ctx := context.Background()
	tctx := ToolContext{SessionID: "test"}

	tests := []struct {
		name    string
		args    string
		wantErr bool
	}{
		{
			name:    "missing topic",
			args:    `{"data":"hello"}`,
			wantErr: true,
		},
		{
			name:    "missing data",
			args:    `{"topic":"test"}`,
			wantErr: true,
		},
		{
			name:    "empty topic",
			args:    `{"topic":"","data":"hello"}`,
			wantErr: true,
		},
		{
			name:    "empty data",
			args:    `{"topic":"test","data":""}`,
			wantErr: true,
		},
		{
			name:    "invalid json",
			args:    `not json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.Execute(ctx, json.RawMessage(tt.args), tctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPipelineQueryESDefaults(t *testing.T) {
	tool := &PipelineQueryESTool{Provider: nil}
	ctx := context.Background()
	tctx := ToolContext{SessionID: "test"}

	// Missing index should error.
	_, err := tool.Execute(ctx, json.RawMessage(`{}`), tctx)
	if err == nil {
		t.Fatal("expected error for missing index")
	}

	// Empty index should error.
	_, err = tool.Execute(ctx, json.RawMessage(`{"index":""}`), tctx)
	if err == nil {
		t.Fatal("expected error for empty index")
	}
}

func TestPipelineUpdateConfigValidation(t *testing.T) {
	tool := &PipelineUpdateConfigTool{Provider: nil}
	ctx := context.Background()
	tctx := ToolContext{SessionID: "test"}

	// Missing config should error.
	_, err := tool.Execute(ctx, json.RawMessage(`{}`), tctx)
	if err == nil {
		t.Fatal("expected error for missing config")
	}

	// Empty config should error.
	_, err = tool.Execute(ctx, json.RawMessage(`{"config":""}`), tctx)
	if err == nil {
		t.Fatal("expected error for empty config")
	}
}

func TestPipelineToolDefinitions(t *testing.T) {
	tools := []Tool{
		&PipelineSendDataTool{},
		&PipelineQueryESTool{},
		&PipelineGetLogstashStatusTool{},
		&PipelineUpdateConfigTool{},
	}

	for _, tool := range tools {
		def := tool.Definition()
		if def.Name == "" {
			t.Fatal("tool has empty name")
		}

		// Verify parameters are valid JSON.
		var params map[string]interface{}
		if err := json.Unmarshal(def.Parameters, &params); err != nil {
			t.Fatalf("tool %s has invalid parameter JSON: %v", def.Name, err)
		}

		// Verify it has a type field.
		if typ, _ := params["type"].(string); typ != "object" {
			t.Fatalf("tool %s parameters should be type=object, got %s", def.Name, typ)
		}
	}
}
