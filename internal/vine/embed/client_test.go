package embed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aspectrr/ivy/internal/vine/config"
)

func fakeEmbeddingResponse(n int, dim int) string {
	data := make([]map[string]interface{}, n)
	for i := range data {
		vec := make([]float32, dim)
		for j := range vec {
			vec[j] = float32(i*dim + j) // deterministic fake values
		}
		data[i] = map[string]interface{}{
			"object":    "embedding",
			"index":     i,
			"embedding": vec,
		}
	}
	resp := map[string]interface{}{
		"object": "list",
		"data":   data,
		"model":  "text-embedding-3-small",
		"usage": map[string]int{
			"prompt_tokens": n * 10,
			"total_tokens":  n * 10,
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

func TestClient_Embed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/embeddings" {
			t.Errorf("expected path /embeddings, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Authorization header, got %s", r.Header.Get("Authorization"))
		}

		var req EmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if req.Input != "hello world" {
			t.Errorf("input = %q, want 'hello world'", req.Input)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, fakeEmbeddingResponse(1, 1536))
	}))
	defer server.Close()

	client := &Client{
		endpoint:   server.URL,
		apiKey:     "test-key",
		model:      "text-embedding-3-small",
		httpClient: server.Client(),
	}

	vec, err := client.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(vec) != 1536 {
		t.Fatalf("vector length = %d, want 1536", len(vec))
	}
	if vec[0] != 0 {
		t.Errorf("vec[0] = %f, want 0", vec[0])
	}
	if vec[1] != 1 {
		t.Errorf("vec[1] = %f, want 1", vec[1])
	}
}

func TestClient_EmbedBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, fakeEmbeddingResponse(3, 1536))
	}))
	defer server.Close()

	client := &Client{
		endpoint:   server.URL,
		apiKey:     "test-key",
		model:      "text-embedding-3-small",
		httpClient: server.Client(),
	}

	texts := []string{"hello", "world", "test"}
	vecs, err := client.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("EmbedBatch() error = %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("expected 3 vectors, got %d", len(vecs))
	}
	for i, vec := range vecs {
		if len(vec) != 1536 {
			t.Errorf("vec[%d] length = %d, want 1536", i, len(vec))
		}
	}
}

func TestClient_EmbedBatch_Empty(t *testing.T) {
	client := &Client{
		endpoint:   "http://unused",
		apiKey:     "test-key",
		model:      "text-embedding-3-small",
		httpClient: &http.Client{},
	}

	vecs, err := client.EmbedBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("EmbedBatch(nil) error = %v", err)
	}
	if vecs != nil {
		t.Errorf("expected nil for empty input, got %v", vecs)
	}
}

func TestClient_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"error":{"message":"Invalid API key","type":"invalid_request_error"}}`)
	}))
	defer server.Close()

	client := &Client{
		endpoint:   server.URL,
		apiKey:     "bad-key",
		model:      "text-embedding-3-small",
		httpClient: server.Client(),
	}

	_, err := client.Embed(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if err.Error() == "" {
		t.Fatal("error should have a message")
	}
}

func TestClient_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"object":"list","data":[],"model":"text-embedding-3-small"}`)
	}))
	defer server.Close()

	client := &Client{
		endpoint:   server.URL,
		apiKey:     "test-key",
		model:      "text-embedding-3-small",
		httpClient: server.Client(),
	}

	_, err := client.Embed(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error for empty data")
	}
}

func TestClient_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		_, _ = fmt.Fprint(w, fakeEmbeddingResponse(1, 1536))
	}))
	defer server.Close()

	client := &Client{
		endpoint:   server.URL,
		apiKey:     "test-key",
		model:      "text-embedding-3-small",
		httpClient: server.Client(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := client.Embed(ctx, "test")
	if err == nil {
		t.Fatal("expected error due to context cancellation")
	}
}

func TestNewClient_DefaultModel(t *testing.T) {
	cfg := config.LLMConfig{
		Endpoint: "http://localhost:8080/v1",
		APIKey:   "test-key",
	}
	client := NewClient(cfg)
	if client.Model() != DefaultEmbeddingModel {
		t.Errorf("model = %q, want %q", client.Model(), DefaultEmbeddingModel)
	}
}

func TestNewClient_CustomModel(t *testing.T) {
	cfg := config.LLMConfig{
		Endpoint:       "http://localhost:8080/v1",
		APIKey:         "test-key",
		EmbeddingModel: "text-embedding-3-large",
	}
	client := NewClient(cfg)
	if client.Model() != "text-embedding-3-large" {
		t.Errorf("model = %q, want text-embedding-3-large", client.Model())
	}
}

func TestClient_SetModel(t *testing.T) {
	client := NewClient(config.LLMConfig{Endpoint: "http://localhost:8080/v1", APIKey: "test"})
	client.SetModel("custom-model")
	if client.Model() != "custom-model" {
		t.Errorf("model = %q, want custom-model", client.Model())
	}
}
