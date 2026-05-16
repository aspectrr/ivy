package vine

import (
	"strings"
	"testing"
)

func TestRewriteKafkaBootstrap(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "single bootstrap_server string",
			input:  `bootstrap_servers => "prod-kafka-01:9092"`,
			expect: `bootstrap_servers => "kafka:9092"`,
		},
		{
			name:   "array of bootstrap_servers",
			input:  `bootstrap_servers => ["kafka1:9092", "kafka2:9092"]`,
			expect: `bootstrap_servers => ["kafka:9092"]`,
		},
		{
			name:   "comma-separated bootstrap_servers",
			input:  `bootstrap_servers => "kafka1:9092,kafka2:9093"`,
			expect: `bootstrap_servers => "kafka:9092"`,
		},
		{
			name: "full input block",
			input: `input {
  kafka {
    bootstrap_servers => "prod-kafka:9092"
    topics => ["logs"]
  }
}`,
			expect: `input {
  kafka {
    bootstrap_servers => "kafka:9092"
    topics => ["logs"]
  }
}`,
		},
		{
			name:   "no bootstrap_servers present",
			input:  `generator { count => 1 }`,
			expect: `generator { count => 1 }`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rewriteKafkaBootstrap(tt.input, "kafka", "9092")
			if result != tt.expect {
				t.Fatalf("expected:\n%s\n\ngot:\n%s", tt.expect, result)
			}
		})
	}
}

func TestRewriteESHosts(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "single host string",
			input:  `hosts => "es-prod:9200"`,
			expect: `hosts => "http://elasticsearch:9200"`,
		},
		{
			name:   "array with http prefix",
			input:  `hosts => ["http://es-prod:9200", "http://es-prod-2:9200"]`,
			expect: `hosts => ["http://elasticsearch:9200"]`,
		},
		{
			name:   "array without http prefix",
			input:  `hosts => ["es-prod:9200"]`,
			expect: `hosts => ["http://elasticsearch:9200"]`,
		},
		{
			name: "full output block",
			input: `output {
  elasticsearch {
    hosts => ["http://es-prod-01:9200"]
    index => "logs-%{+YYYY.MM.dd}"
  }
}`,
			expect: `output {
  elasticsearch {
    hosts => ["http://elasticsearch:9200"]
    index => "logs-%{+YYYY.MM.dd}"
  }
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rewriteESHosts(tt.input, "elasticsearch", "9200")
			if result != tt.expect {
				t.Fatalf("expected:\n%s\n\ngot:\n%s", tt.expect, result)
			}
		})
	}
}

func TestRewriteLogstashConfig(t *testing.T) {
	input := `input {
  kafka {
    bootstrap_servers => "prod-kafka-01:9092,prod-kafka-02:9092"
    topics => ["apache-logs"]
    group_id => "logstash-prod"
  }
}

filter {
  grok {
    match => { "message" => "%{COMBINEDAPACHELOG}" }
  }
  date {
    match => [ "timestamp", "dd/MMM/yyyy:HH:mm:ss Z" ]
  }
}

output {
  elasticsearch {
    hosts => ["http://es-prod-01:9200", "http://es-prod-02:9200"]
    index => "apache-logs-%{+YYYY.MM.dd}"
  }
}
`

	result := RewriteLogstashConfig(input)

	// Verify Kafka bootstrap_servers was rewritten.
	if !strings.Contains(result, `bootstrap_servers => "kafka:9092"`) {
		t.Fatal("expected Kafka bootstrap_servers rewritten to kafka:9092")
	}
	if strings.Contains(result, "prod-kafka") {
		t.Fatal("expected prod-kafka references to be removed")
	}

	// Verify ES hosts were rewritten.
	if !strings.Contains(result, `hosts => ["http://elasticsearch:9200"]`) {
		t.Fatal("expected ES hosts rewritten to elasticsearch:9200")
	}
	if strings.Contains(result, "es-prod") {
		t.Fatal("expected es-prod references to be removed")
	}

	// Verify filters are preserved.
	if !strings.Contains(result, "grok") {
		t.Fatal("expected grok filter to be preserved")
	}
	if !strings.Contains(result, "COMBINEDAPACHELOG") {
		t.Fatal("expected COMBINEDAPACHELOG pattern to be preserved")
	}
	if !strings.Contains(result, "date") {
		t.Fatal("expected date filter to be preserved")
	}

	// Verify other settings are preserved.
	if !strings.Contains(result, `topics => ["apache-logs"]`) {
		t.Fatal("expected topics to be preserved")
	}
	if !strings.Contains(result, `index => "apache-logs-%{+YYYY.MM.dd}"`) {
		t.Fatal("expected index pattern to be preserved")
	}
}

func TestRewriteWithCustomAddrs(t *testing.T) {
	input := `input {
  kafka {
    bootstrap_servers => "prod:9092"
  }
}
output {
  elasticsearch {
    hosts => ["http://prod:9200"]
  }
}`

	result := RewriteLogstashConfig(input,
		WithKafkaAddr("my-kafka", "9199"),
		WithESAddr("my-es", "9299"),
	)

	if !strings.Contains(result, `bootstrap_servers => "my-kafka:9199"`) {
		t.Fatalf("expected my-kafka:9199, got: %s", result)
	}
	if !strings.Contains(result, `hosts => ["http://my-es:9299"]`) {
		t.Fatalf("expected http://my-es:9299, got: %s", result)
	}
}

func TestValidateLogstashConfig(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name: "valid input+output",
			input: `input { kafka { bootstrap_servers => "kafka:9092" } }
output { elasticsearch { hosts => ["http://es:9200"] } }`,
			wantErr: false,
		},
		{
			name: "valid input only",
			input: `input {
  generator { count => 1 }
}`,
			wantErr: false,
		},
		{
			name:    "empty config",
			input:   "",
			wantErr: true,
		},
		{
			name:    "whitespace only",
			input:   "   \n\t  ",
			wantErr: true,
		},
		{
			name:    "unbalanced braces - extra close",
			input:   `input { kafka {} }}`,
			wantErr: true,
		},
		{
			name:    "unbalanced braces - unclosed",
			input:   `input { kafka { }`,
			wantErr: true,
		},
		{
			name:    "no input or output block",
			input:   `filter { mutate { add_field => { "foo" => "bar" } } }`,
			wantErr: true,
		},
		{
			name: "valid with filter",
			input: `input { kafka { bootstrap_servers => "kafka:9092" } }
filter { grok { match => { "message" => "%{GREEDYDATA}" } } }
output { elasticsearch { hosts => ["http://es:9200"] } }`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLogstashConfig(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateLogstashConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDefaultLogstashConfig(t *testing.T) {
	cfg := defaultLogstashConfig()
	if !strings.Contains(cfg, "input {") {
		t.Fatal("default config missing input block")
	}
	if !strings.Contains(cfg, "output {") {
		t.Fatal("default config missing output block")
	}
	if !strings.Contains(cfg, "kafka") {
		t.Fatal("default config missing kafka input")
	}
	if !strings.Contains(cfg, "elasticsearch") {
		t.Fatal("default config missing elasticsearch output")
	}
}

func TestEscapeForShell(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"hello world", "hello world"},
		{"it's a test", "it'\\''s a test"},
		{"plain", "plain"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := escapeForShell(tt.input)
			if result != tt.expect {
				t.Fatalf("expected %q, got %q", tt.expect, result)
			}
		})
	}
}

func TestStripDockerLogHeaders(t *testing.T) {
	input := []byte{
		1, 0, 0, 0, 5, 0, 0, 0, 'h', 'e', 'l', 'l', 'o',
		2, 0, 0, 0, 3, 0, 0, 0, 'e', 'r', 'r',
	}

	result := stripDockerLogHeaders(input)
	if !strings.Contains(result, "hello") {
		t.Fatalf("expected 'hello' in result, got %q", result)
	}
	if !strings.Contains(result, "err") {
		t.Fatalf("expected 'err' in result, got %q", result)
	}
}
