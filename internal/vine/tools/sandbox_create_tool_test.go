package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aspectrr/ivy/internal/ivyv1"
	"github.com/aspectrr/ivy/internal/vine/vine"
)

// --- Mocks ---

type mockSandboxCreator struct {
	created bool
	cfg     vine.PipelineConfig
	err     error
}

func (m *mockSandboxCreator) CreatePipeline(_ context.Context, sessionID string, cfg vine.PipelineConfig) (*vine.PipelineSandbox, error) {
	if m.err != nil {
		return nil, m.err
	}
	m.created = true
	m.cfg = cfg
	return &vine.PipelineSandbox{SessionID: sessionID}, nil
}

type mockLeafRunner struct {
	responses map[string]*ivyv1.CommandOutput // command pattern → response
	err       error
}

func (m *mockLeafRunner) SendCommandAndWait(_ context.Context, _ string, req *ivyv1.ExecuteCommandRequest) (*ivyv1.CommandOutput, error) {
	if m.err != nil {
		return nil, m.err
	}

	// Match based on command type + args
	key := commandKey(req)
	if resp, ok := m.responses[key]; ok {
		return resp, nil
	}

	// Fallback: return empty success
	return &ivyv1.CommandOutput{ExitCode: 0}, nil
}

func commandKey(req *ivyv1.ExecuteCommandRequest) string {
	args := strings.Join(req.Args, " ")
	return req.Command.String() + ":" + args
}

// --- Tests ---

func TestCreateSandboxToolRegistered(t *testing.T) {
	reg := NewRegistry()
	tool := &CreateSandboxTool{
		Creator:    &mockSandboxCreator{},
		LeafRunner: &mockLeafRunner{},
	}
	if err := reg.Register(tool); err != nil {
		t.Fatalf("Register create_sandbox: %v", err)
	}

	got, err := reg.Get("create_sandbox")
	if err != nil {
		t.Fatalf("Get create_sandbox: %v", err)
	}

	def := got.Definition()
	if def.Name != "create_sandbox" {
		t.Fatalf("expected name create_sandbox, got %s", def.Name)
	}
	if def.Description == "" {
		t.Fatal("empty description")
	}
	if !strings.Contains(def.Description, "list_parser_hosts") {
		t.Fatal("description should mention list_parser_hosts")
	}

	// Verify parameters are valid JSON
	var params map[string]interface{}
	if err := json.Unmarshal(def.Parameters, &params); err != nil {
		t.Fatalf("invalid parameter JSON: %v", err)
	}
}

func TestCreateSandboxMissingHostID(t *testing.T) {
	tool := &CreateSandboxTool{
		Creator:    &mockSandboxCreator{},
		LeafRunner: &mockLeafRunner{},
	}

	_, err := tool.Execute(context.Background(), json.RawMessage(`{}`), ToolContext{SessionID: "test"})
	if err == nil {
		t.Fatal("expected error for missing host_id")
	}
	if !strings.Contains(err.Error(), "host_id") {
		t.Fatalf("error should mention host_id, got: %v", err)
	}
}

func TestCreateSandboxVersionDetection(t *testing.T) {
	// Common config responses for all test cases
	configResponses := map[string]*ivyv1.CommandOutput{
		"FIND:/etc/logstash/conf.d -name *.conf -type f": {ExitCode: 0, Stdout: "/etc/logstash/conf.d/test.conf\n"},
		"CAT:/etc/logstash/conf.d/test.conf":             {ExitCode: 0, Stdout: "input { kafka {} }"},
	}
	tests := []struct {
		name            string
		responses       map[string]*ivyv1.CommandOutput
		expectedVersion string
		wantErr         bool
	}{
		{
			name: "version file",
			responses: map[string]*ivyv1.CommandOutput{
				"CAT:/usr/share/logstash/VERSION": {ExitCode: 0, Stdout: "8.15.0\n"},
			},
			expectedVersion: "8.15.0",
		},
		{
			name: "systemctl status fallback",
			responses: map[string]*ivyv1.CommandOutput{
				"CAT:/usr/share/logstash/VERSION": {ExitCode: 1, Stderr: "not found"},
				"SYSTEMCTL_STATUS:logstash":       {ExitCode: 0, Stdout: "● logstash.service - Logstash\n   Loaded: loaded\n   Active: active (running)\n Main PID: 1234 (java)\n   CGroup: /system.slice/logstash.service\n           └─1234 /usr/bin/java -Xms1g -Xmx1g -Dls.cgroup.enabled=false -Dpath.logs=/var/log/logstash -Dpath.settings=/etc/logstash -cp /usr/share/logstash/logstash-core/lib/jars/*:/usr/share/logstash/vendor/bundle/jruby/3.1.0/gems/*.jar -m org.logstash.Logstash logstash/8.17.0"},
			},
			expectedVersion: "8.17.0",
		},
		{
			name: "grep gem version fallback",
			responses: map[string]*ivyv1.CommandOutput{
				"CAT:/usr/share/logstash/VERSION": {ExitCode: 1},
				"SYSTEMCTL_STATUS:logstash":       {ExitCode: 1},
				"GREP:-r LOGSTASH_VERSION /usr/share/logstash/logstash-core/lib/gem_version.rb": {ExitCode: 0, Stdout: "  LOGSTASH_VERSION = \"9.1.2\"\n"},
			},
			expectedVersion: "9.1.2",
		},
		{
			name: "all methods fail",
			responses: map[string]*ivyv1.CommandOutput{
				"CAT:/usr/share/logstash/VERSION": {ExitCode: 1},
				"SYSTEMCTL_STATUS:logstash":       {ExitCode: 1},
				"GREP:-r LOGSTASH_VERSION /usr/share/logstash/logstash-core/lib/gem_version.rb": {ExitCode: 1},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Merge config responses into test responses
			merged := make(map[string]*ivyv1.CommandOutput)
			for k, v := range configResponses {
				merged[k] = v
			}
			for k, v := range tt.responses {
				merged[k] = v
			}

			creator := &mockSandboxCreator{}
			runner := &mockLeafRunner{responses: merged}

			tool := &CreateSandboxTool{
				Creator:    creator,
				LeafRunner: runner,
			}

			result, err := tool.Execute(context.Background(),
				json.RawMessage(`{"host_id":"test-host-1"}`),
				ToolContext{SessionID: "sess-1"},
			)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), "Logstash version") {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !creator.created {
				t.Fatal("sandbox was not created")
			}

			var resp map[string]interface{}
			if err := json.Unmarshal(result, &resp); err != nil {
				t.Fatalf("parsing result: %v", err)
			}

			if v, _ := resp["logstash_version"].(string); v != tt.expectedVersion {
				t.Fatalf("expected version %s, got %s", tt.expectedVersion, v)
			}

			if creator.cfg.LogstashImage != "docker.elastic.co/logstash/logstash:"+tt.expectedVersion {
				t.Fatalf("expected image with version %s, got %s", tt.expectedVersion, creator.cfg.LogstashImage)
			}
		})
	}
}

func TestCreateSandboxConfigReading(t *testing.T) {
	responses := map[string]*ivyv1.CommandOutput{
		"CAT:/usr/share/logstash/VERSION":                {ExitCode: 0, Stdout: "8.15.0\n"},
		"FIND:/etc/logstash/conf.d -name *.conf -type f": {ExitCode: 0, Stdout: "/etc/logstash/conf.d/01-input.conf\n/etc/logstash/conf.d/02-filter.conf\n"},
		"CAT:/etc/logstash/conf.d/01-input.conf":         {ExitCode: 0, Stdout: "input { kafka { bootstrap_servers => \"kafka:9092\" } }"},
		"CAT:/etc/logstash/conf.d/02-filter.conf":        {ExitCode: 0, Stdout: "filter { grok { match => { \"message\" => \"%{IP:client}\" } } }"},
	}

	creator := &mockSandboxCreator{}
	runner := &mockLeafRunner{responses: responses}

	tool := &CreateSandboxTool{
		Creator:    creator,
		LeafRunner: runner,
	}

	_, err := tool.Execute(context.Background(),
		json.RawMessage(`{"host_id":"test-host-1"}`),
		ToolContext{SessionID: "sess-1"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !creator.created {
		t.Fatal("sandbox was not created")
	}

	// Verify config contains both files
	if !strings.Contains(creator.cfg.LogstashConfig, "01-input.conf") {
		t.Fatal("config should reference 01-input.conf")
	}
	if !strings.Contains(creator.cfg.LogstashConfig, "kafka") {
		t.Fatal("config should contain kafka input")
	}
	if !strings.Contains(creator.cfg.LogstashConfig, "grok") {
		t.Fatal("config should contain grok filter")
	}
}

func TestCreateSandboxNoConfigFiles(t *testing.T) {
	responses := map[string]*ivyv1.CommandOutput{
		"CAT:/usr/share/logstash/VERSION":                         {ExitCode: 0, Stdout: "8.15.0\n"},
		"FIND:/etc/logstash/conf.d -name *.conf -type f":          {ExitCode: 0, Stdout: ""},
		"FIND:/etc/logstash/pipeline.conf.d -name *.conf -type f": {ExitCode: 0, Stdout: ""},
		"FIND:/etc/logstash -name *.conf -type f":                 {ExitCode: 0, Stdout: ""},
	}

	creator := &mockSandboxCreator{}
	runner := &mockLeafRunner{responses: responses}

	tool := &CreateSandboxTool{
		Creator:    creator,
		LeafRunner: runner,
	}

	_, err := tool.Execute(context.Background(),
		json.RawMessage(`{"host_id":"test-host-1"}`),
		ToolContext{SessionID: "sess-1"},
	)
	if err == nil {
		t.Fatal("expected error when no config files found")
	}
	if !strings.Contains(err.Error(), "no .conf files") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateSandboxCreatorError(t *testing.T) {
	responses := map[string]*ivyv1.CommandOutput{
		"CAT:/usr/share/logstash/VERSION":                {ExitCode: 0, Stdout: "8.15.0\n"},
		"FIND:/etc/logstash/conf.d -name *.conf -type f": {ExitCode: 0, Stdout: "/etc/logstash/conf.d/test.conf\n"},
		"CAT:/etc/logstash/conf.d/test.conf":             {ExitCode: 0, Stdout: "input {}"},
	}

	creator := &mockSandboxCreator{err: fmt.Errorf("docker network create failed")}
	runner := &mockLeafRunner{responses: responses}

	tool := &CreateSandboxTool{
		Creator:    creator,
		LeafRunner: runner,
	}

	_, err := tool.Execute(context.Background(),
		json.RawMessage(`{"host_id":"test-host-1"}`),
		ToolContext{SessionID: "sess-1"},
	)
	if err == nil {
		t.Fatal("expected error from creator")
	}
	if !strings.Contains(err.Error(), "creating pipeline sandbox") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateSandboxLeafError(t *testing.T) {
	creator := &mockSandboxCreator{}
	runner := &mockLeafRunner{err: fmt.Errorf("leaf not connected")}

	tool := &CreateSandboxTool{
		Creator:    creator,
		LeafRunner: runner,
	}

	_, err := tool.Execute(context.Background(),
		json.RawMessage(`{"host_id":"test-host-1"}`),
		ToolContext{SessionID: "sess-1"},
	)
	if err == nil {
		t.Fatal("expected error when leaf is unreachable")
	}
	if creator.created {
		t.Fatal("sandbox should not have been created")
	}
}

func TestCreateSandboxInvalidJSON(t *testing.T) {
	tool := &CreateSandboxTool{
		Creator:    &mockSandboxCreator{},
		LeafRunner: &mockLeafRunner{},
	}

	_, err := tool.Execute(context.Background(),
		json.RawMessage(`not json`),
		ToolContext{SessionID: "sess-1"},
	)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
