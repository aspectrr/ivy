// Package embed provides an OpenAI-compatible embeddings client
// for generating vector embeddings used with pgvector similarity search.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/aspectrr/ivy/internal/vine/config"
)

// DefaultEmbeddingModel is the default model used for embeddings.
const DefaultEmbeddingModel = "text-embedding-3-small"

// Vector is a float32 slice representing an embedding vector.
type Vector = []float32

// Client generates embeddings via an OpenAI-compatible /embeddings endpoint.
type Client struct {
	endpoint   string
	apiKey     string
	model      string
	httpClient *http.Client
}

// EmbeddingRequest is the request body for /embeddings.
type EmbeddingRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

// EmbeddingResponse is the response from /embeddings.
type EmbeddingResponse struct {
	Object string          `json:"object"`
	Data   []EmbeddingData `json:"data"`
	Model  string          `json:"model"`
	Usage  EmbeddingUsage  `json:"usage"`
}

// EmbeddingData holds a single embedding vector.
type EmbeddingData struct {
	Object    string `json:"object"`
	Index     int    `json:"index"`
	Embedding Vector `json:"embedding"`
}

// EmbeddingUsage tracks token counts for the embedding request.
type EmbeddingUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// NewClient creates a new embedding client from LLM config.
// It uses the same endpoint and API key as the chat LLM client,
// with a separate embedding_model config field.
func NewClient(cfg config.LLMConfig) *Client {
	model := cfg.EmbeddingModel
	if model == "" {
		model = DefaultEmbeddingModel
	}
	return &Client{
		endpoint:   strings.TrimRight(cfg.Endpoint, "/"),
		apiKey:     cfg.APIKey,
		model:      model,
		httpClient: &http.Client{},
	}
}

// SetModel overrides the embedding model for subsequent requests.
func (c *Client) SetModel(model string) {
	c.model = model
}

// Model returns the current embedding model name.
func (c *Client) Model() string {
	return c.model
}

// Embed generates a single embedding vector for the given text.
func (c *Client) Embed(ctx context.Context, text string) (Vector, error) {
	req := EmbeddingRequest{
		Model: c.model,
		Input: text,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var embResp EmbeddingResponse
	if err := json.Unmarshal(respBody, &embResp); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}

	if len(embResp.Data) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}

	return embResp.Data[0].Embedding, nil
}

// EmbedBatch generates embeddings for multiple texts in a single request.
// Returns embeddings in the same order as the input texts.
func (c *Client) EmbedBatch(ctx context.Context, texts []string) ([]Vector, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// Build request with array input
	type batchRequest struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}

	req := batchRequest{
		Model: c.model,
		Input: texts,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var embResp EmbeddingResponse
	if err := json.Unmarshal(respBody, &embResp); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}

	// Sort by index to ensure order matches input
	result := make([]Vector, len(texts))
	for _, d := range embResp.Data {
		if d.Index < 0 || d.Index >= len(result) {
			continue
		}
		result[d.Index] = d.Embedding
	}

	return result, nil
}
