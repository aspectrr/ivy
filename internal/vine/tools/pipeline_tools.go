package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aspectrr/ivy/internal/vine/vine"
)

// PipelineProvider is the interface tools use to access pipeline sandboxes.
type PipelineProvider interface {
	GetPipeline(sessionID string) (*vine.PipelineSandbox, error)
}

// --- pipeline_send_data ---

type PipelineSendDataTool struct {
	Provider PipelineProvider
}

func (t *PipelineSendDataTool) Definition() ToolDef {
	return ToolDef{
		Name:        "pipeline_send_data",
		Description: "Send test data through the pipeline's Kafka broker. The data will be processed by Logstash and indexed in Elasticsearch. Requires a sandbox — call create_sandbox first.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"topic":  {"type": "string", "description": "Kafka topic to send data to"},
				"data":   {"type": "string", "description": "Data to send (plain text, JSON, or syslog format)"},
				"format": {"type": "string", "enum": ["plain", "json", "syslog"], "description": "Format hint for the data (default: auto-detect)"}
			},
			"required": ["topic", "data"]
		}`),
	}
}

func (t *PipelineSendDataTool) Execute(ctx context.Context, args json.RawMessage, tctx ToolContext) (json.RawMessage, error) {
	if t.Provider == nil {
		return nil, fmt.Errorf("pipeline not available")
	}

	var params struct {
		Topic  string `json:"topic"`
		Data   string `json:"data"`
		Format string `json:"format"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("parsing args: %w", err)
	}

	if params.Topic == "" {
		return nil, fmt.Errorf("topic is required")
	}
	if params.Data == "" {
		return nil, fmt.Errorf("data is required")
	}

	ps, err := t.Provider.GetPipeline(tctx.SessionID)
	if err != nil {
		return nil, fmt.Errorf("no pipeline sandbox for session: %w", err)
	}

	if err := ps.SendData(ctx, params.Topic, params.Data); err != nil {
		return nil, fmt.Errorf("sending data to Kafka: %w", err)
	}

	return json.Marshal(map[string]string{
		"status":  "sent",
		"topic":   params.Topic,
		"message": fmt.Sprintf("Data sent to topic '%s'. Wait a few seconds for Logstash to process before querying.", params.Topic),
	})
}

// --- pipeline_query_es ---

type PipelineQueryESTool struct {
	Provider PipelineProvider
}

func (t *PipelineQueryESTool) Definition() ToolDef {
	return ToolDef{
		Name:        "pipeline_query_es",
		Description: "Query the pipeline's Elasticsearch instance to verify data was processed. Use after pipeline_send_data to check results. Requires a sandbox — call create_sandbox first.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"index": {"type": "string", "description": "Elasticsearch index to query (e.g., 'test-events')"},
				"query": {"type": "string", "description": "ES query DSL (JSON) or plain text search string"}
			},
			"required": ["index"]
		}`),
	}
}

func (t *PipelineQueryESTool) Execute(ctx context.Context, args json.RawMessage, tctx ToolContext) (json.RawMessage, error) {
	if t.Provider == nil {
		return nil, fmt.Errorf("pipeline not available")
	}

	var params struct {
		Index string `json:"index"`
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("parsing args: %w", err)
	}

	if params.Index == "" {
		return nil, fmt.Errorf("index is required")
	}

	if params.Query == "" {
		params.Query = `{"query":{"match_all":{}}}`
	}

	ps, err := t.Provider.GetPipeline(tctx.SessionID)
	if err != nil {
		return nil, fmt.Errorf("no pipeline sandbox for session: %w", err)
	}

	result, err := ps.QueryES(ctx, params.Index, params.Query)
	if err != nil {
		return nil, fmt.Errorf("querying ES: %w", err)
	}

	return result, nil
}

// --- pipeline_get_logstash_status ---

type PipelineGetLogstashStatusTool struct {
	Provider PipelineProvider
}

func (t *PipelineGetLogstashStatusTool) Definition() ToolDef {
	return ToolDef{
		Name:        "pipeline_get_logstash_status",
		Description: "Get Logstash logs and status from the pipeline sandbox. Useful for debugging pipeline processing issues. Requires a sandbox — call create_sandbox first.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"tail": {"type": "string", "description": "Number of log lines to return (default: 50)"}
			},
			"required": []
		}`),
	}
}

func (t *PipelineGetLogstashStatusTool) Execute(ctx context.Context, args json.RawMessage, tctx ToolContext) (json.RawMessage, error) {
	if t.Provider == nil {
		return nil, fmt.Errorf("pipeline not available")
	}

	var params struct {
		Tail string `json:"tail"`
	}
	_ = json.Unmarshal(args, &params)

	ps, err := t.Provider.GetPipeline(tctx.SessionID)
	if err != nil {
		return nil, fmt.Errorf("no pipeline sandbox for session: %w", err)
	}

	logs, err := ps.GetLogstashLogs(ctx, params.Tail)
	if err != nil {
		return nil, fmt.Errorf("getting Logstash logs: %w", err)
	}

	return json.Marshal(map[string]string{
		"logs":    logs,
		"message": "Logstash logs retrieved. Look for errors or pipeline processing info.",
	})
}

// --- pipeline_update_config ---

type PipelineUpdateConfigTool struct {
	Provider PipelineProvider
}

func (t *PipelineUpdateConfigTool) Definition() ToolDef {
	return ToolDef{
		Name:        "pipeline_update_config",
		Description: "Update the Logstash pipeline configuration and restart Logstash. The config is automatically rewritten to use Docker service names. Requires a sandbox — call create_sandbox first.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"config": {"type": "string", "description": "New Logstash pipeline configuration (input/filter/output blocks)"}
			},
			"required": ["config"]
		}`),
	}
}

func (t *PipelineUpdateConfigTool) Execute(ctx context.Context, args json.RawMessage, tctx ToolContext) (json.RawMessage, error) {
	if t.Provider == nil {
		return nil, fmt.Errorf("pipeline not available")
	}

	var params struct {
		Config string `json:"config"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("parsing args: %w", err)
	}

	if params.Config == "" {
		return nil, fmt.Errorf("config is required")
	}

	ps, err := t.Provider.GetPipeline(tctx.SessionID)
	if err != nil {
		return nil, fmt.Errorf("no pipeline sandbox for session: %w", err)
	}

	if err := ps.UpdateLogstashConfig(ctx, params.Config); err != nil {
		return nil, fmt.Errorf("updating Logstash config: %w", err)
	}

	return json.Marshal(map[string]string{
		"status":  "updated",
		"message": "Logstash config updated and container restarted. Wait a few seconds for it to reconnect to Kafka.",
	})
}

// --- pipeline_health ---

type PipelineHealthTool struct {
	Provider PipelineProvider
}

func (t *PipelineHealthTool) Definition() ToolDef {
	return ToolDef{
		Name:        "pipeline_health",
		Description: "Check the health of all pipeline components (Redpanda, Elasticsearch, Logstash). Returns per-component status and an overall health assessment. Requires a sandbox — call create_sandbox first.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {},
			"required": []
		}`),
	}
}

func (t *PipelineHealthTool) Execute(ctx context.Context, _ json.RawMessage, tctx ToolContext) (json.RawMessage, error) {
	if t.Provider == nil {
		return nil, fmt.Errorf("pipeline not available")
	}

	ps, err := t.Provider.GetPipeline(tctx.SessionID)
	if err != nil {
		return nil, fmt.Errorf("no pipeline sandbox for session: %w", err)
	}

	report, err := ps.Health(ctx)
	if err != nil {
		return nil, fmt.Errorf("health check failed: %w", err)
	}

	return json.Marshal(report)
}

// RegisterPipelineTools registers all pipeline tools.
func RegisterPipelineTools(registry *Registry, provider PipelineProvider) error {
	tools := []Tool{
		&PipelineSendDataTool{Provider: provider},
		&PipelineQueryESTool{Provider: provider},
		&PipelineGetLogstashStatusTool{Provider: provider},
		&PipelineUpdateConfigTool{Provider: provider},
		&PipelineHealthTool{Provider: provider},
	}
	for _, tool := range tools {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	return nil
}
