package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// SkillStore is the interface the skill tools use to access skills.
// This decouples the tools from the actual database-backed store.
type SkillStore interface {
	// ListSkills returns all skills with name and description.
	ListSkills(ctx context.Context) ([]SkillSummary, error)
	// GetSkill returns the full skill content by name.
	GetSkill(ctx context.Context, name string) (*SkillContent, error)
}

// SkillSummary is a brief overview of a skill for listing.
type SkillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	BuiltIn     bool   `json:"built_in"`
}

// SkillContent is the full skill with its instructions.
type SkillContent struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	BuiltIn     bool   `json:"built_in"`
}

// memorySkillStore is an in-memory skill store for testing and bootstrapping.
type memorySkillStore struct {
	mu     sync.RWMutex
	skills map[string]SkillContent
}

// NewMemorySkillStore creates an in-memory skill store preloaded with built-in skills.
func NewMemorySkillStore() SkillStore {
	s := &memorySkillStore{
		skills: make(map[string]SkillContent),
	}

	// Seed with built-in skills.
	builtIn := []SkillContent{
		{
			Name:        "kafka-debugging",
			Description: "Patterns for debugging Kafka consumer lag, partition rebalancing, and broker issues.",
			Content: `# Kafka Debugging Skills

## Consumer Lag Investigation
1. Check consumer group lag: use kafka-consumer-groups --describe
2. Look for rebalancing events in broker logs
3. Verify partition assignment is even
4. Check if consumers are actually processing (look at offset commit rate)

## Common Issues
- **Slow consumers**: Check GC pauses, network latency to broker, deserialization overhead
- **Rebalance storms**: Often caused by consumers taking longer than max.poll.interval.ms
- **Under-replicated partitions**: Check ISR count, broker disk space, network between brokers`,
			BuiltIn: true,
		},
		{
			Name:        "elasticsearch-query-patterns",
			Description: "Useful ES query patterns for log analysis, mapping debugging, and index management.",
			Content: `# Elasticsearch Query Patterns

## Debugging Mappings
- GET /index/_mapping — check current mapping
- Use _analyze API to test analyzers: POST /index/_analyze { "text": "...", "analyzer": "..." }
- Common mapping conflicts: trying to change field type, auto-detected integer as long

## Log Search Patterns
- Range queries for time windows: {"range": {"@timestamp": {"gte": "now-1h"}}}
- Aggregation for error rates: terms agg on "level" field
- Use bool/must for AND, bool/should for OR`,
			BuiltIn: true,
		},
		{
			Name:        "logstash-config-patterns",
			Description: "Logstash config patterns, grok debugging, and pipeline troubleshooting.",
			Content: `# Logstash Config Patterns

## Grok Debugging
- Use the grokdebugger in Kibana Dev Tools
- Start with %{GREEDYDATA:message} and narrow down
- Common pattern: %{TIMESTAMP_ISO8601:timestamp} %{LOGLEVEL:level} %{GREEDYDATA:message}
- Test with: Logstash --config.test_and_exit -f pipeline.conf

## Pipeline Patterns
- Use multiline codec for stack traces
- Use mutate to rename/remove fields
- Use date filter to parse timestamps into @timestamp
- Always set pipeline.workers and pipeline.batch.size`,
			BuiltIn: true,
		},
		{
			Name:        "sysadmin-debugging",
			Description: "Common system debugging patterns: disk, memory, network, process investigation.",
			Content: `# System Debugging Patterns

## Disk Issues
- df -h for space, du -sh * for culprits, lsof +L1 for deleted files still open
- Check inode usage: df -i

## Memory
- free -h for overview, top/htop for per-process
- Check for OOM kills: dmesg | grep -i oom
- Check swap usage and swappiness

## Network
- ss -tlnp for listening ports
- netstat -an | grep ESTABLISHED for connections
- Check DNS: dig, nslookup
- Check firewall: iptables -L -n`,
			BuiltIn: true,
		},
		{
			Name:        "create-skill",
			Description: "Instructions for creating a new skill after completing a session. Reflect on what you did, what worked, and what didn't.",
			Content: `# Creating a New Skill

After completing a debugging session, create a skill so future agents can learn from your experience.

## Steps
1. **Reflect**: What was the problem? What did you try? What worked? What didn't?
2. **Summarize**: Write a concise skill with:
   - Clear title describing the scenario
   - Brief description for searchability
   - Step-by-step approach that worked
   - Pitfalls to avoid
3. **Name it**: Use a descriptive name like "kafka-consumer-lag-investigation" or "logstash-grok-debugging-workflow"
4. **Be specific**: Include actual commands, config snippets, or query patterns that were useful

## Example
Name: "logstash-multiline-stacktrace"
Description: "How to configure Logstash to handle multiline Java stack traces"
Content: Use the multiline codec with pattern => "^\s" (lines starting with whitespace are continuation)`,
			BuiltIn: true,
		},
	}

	for _, skill := range builtIn {
		s.skills[skill.Name] = skill
	}

	return s
}

func (m *memorySkillStore) ListSkills(_ context.Context) ([]SkillSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]SkillSummary, 0, len(m.skills))
	for _, s := range m.skills {
		result = append(result, SkillSummary{
			Name:        s.Name,
			Description: s.Description,
			BuiltIn:     s.BuiltIn,
		})
	}
	return result, nil
}

func (m *memorySkillStore) GetSkill(_ context.Context, name string) (*SkillContent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	skill, ok := m.skills[name]
	if !ok {
		return nil, fmt.Errorf("skill %q not found", name)
	}
	return &skill, nil
}

// --- list_skills tool ---

type ListSkillsTool struct {
	Store SkillStore
}

func (t *ListSkillsTool) Definition() ToolDef {
	return ToolDef{
		Name:        "list_skills",
		Description: "List all available skills. Use this at the start of a session to discover relevant skills for your task.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
	}
}

func (t *ListSkillsTool) Execute(ctx context.Context, _ json.RawMessage, _ ToolContext) (json.RawMessage, error) {
	if t.Store == nil {
		return nil, fmt.Errorf("skill store not available")
	}

	skills, err := t.Store.ListSkills(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing skills: %w", err)
	}

	if len(skills) == 0 {
		return json.Marshal(map[string]interface{}{
			"skills":  []interface{}{},
			"message": "No skills available yet. Skills are created after completing sessions.",
		})
	}

	// Format as a readable list for the agent.
	var b strings.Builder
	fmt.Fprintf(&b, "Available skills (%d):\n\n", len(skills))
	for _, s := range skills {
		badge := ""
		if s.BuiltIn {
			badge = " [built-in]"
		}
		fmt.Fprintf(&b, "- **%s**%s: %s\n", s.Name, badge, s.Description)
	}
	b.WriteString("\nUse `get_skill` to load the full content of a skill.")

	return json.Marshal(map[string]string{
		"skills":  b.String(),
		"message": fmt.Sprintf("Found %d skills. Use get_skill to load one.", len(skills)),
	})
}

// --- get_skill tool ---

type GetSkillTool struct {
	Store SkillStore
}

func (t *GetSkillTool) Definition() ToolDef {
	return ToolDef{
		Name:        "get_skill",
		Description: "Load the full content of a skill by name. Use this after list_skills to pull in specific skills relevant to your task.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Exact skill name from list_skills output"}},"required":["name"]}`),
	}
}

func (t *GetSkillTool) Execute(ctx context.Context, args json.RawMessage, _ ToolContext) (json.RawMessage, error) {
	if t.Store == nil {
		return nil, fmt.Errorf("skill store not available")
	}

	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("parsing args: %w", err)
	}

	if params.Name == "" {
		return nil, fmt.Errorf("skill name is required")
	}

	skill, err := t.Store.GetSkill(ctx, params.Name)
	if err != nil {
		return nil, err
	}

	return json.Marshal(map[string]string{
		"name":        skill.Name,
		"description": skill.Description,
		"content":     skill.Content,
	})
}

// RegisterSkillTools registers the list_skills and get_skill tools.
// The store can be nil (tools will return errors) — wire a real store in Phase 5.
func RegisterSkillTools(registry *Registry, store SkillStore) error {
	if err := registry.Register(&ListSkillsTool{Store: store}); err != nil {
		return err
	}
	return registry.Register(&GetSkillTool{Store: store})
}
