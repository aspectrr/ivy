package database

import (
	"context"
	"testing"

	"github.com/aspectrr/ivy/internal/vine/config"
)

func TestPoolConnection(t *testing.T) {
	cfg := config.DatabaseConfig{
		Host:     "localhost",
		Port:     5432,
		Name:     "ivy",
		User:     "ivy",
		Password: "ivy",
		SSLMode:  "disable",
	}

	pool, err := NewPool(context.Background(), cfg)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("failed to ping: %v", err)
	}

	t.Log("database connection pool created and pinged successfully")
}

func TestEnsureEmbeddingDim(t *testing.T) {
	cfg := config.DatabaseConfig{
		Host:     "localhost",
		Port:     5432,
		Name:     "ivy",
		User:     "ivy",
		Password: "ivy",
		SSLMode:  "disable",
	}

	pool, err := NewPool(context.Background(), cfg)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()

	// Should work with the default 1536 dims from schema
	err = EnsureEmbeddingDim(ctx, pool, 1536)
	if err != nil {
		t.Fatalf("EnsureEmbeddingDim(1536): %v", err)
	}

	// Now change to 768 (Ollama nomic-embed-text)
	err = EnsureEmbeddingDim(ctx, pool, 768)
	if err != nil {
		t.Fatalf("EnsureEmbeddingDim(768): %v", err)
	}

	// Change back to 1536 to restore original state
	err = EnsureEmbeddingDim(ctx, pool, 1536)
	if err != nil {
		t.Fatalf("EnsureEmbeddingDim(1536 restore): %v", err)
	}

	t.Log("embedding dimension switching works correctly")
}

func TestEnsureEmbeddingDim_Noop(t *testing.T) {
	cfg := config.DatabaseConfig{
		Host:     "localhost",
		Port:     5432,
		Name:     "ivy",
		User:     "ivy",
		Password: "ivy",
		SSLMode:  "disable",
	}

	pool, err := NewPool(context.Background(), cfg)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer pool.Close()

	// dim=0 should be a no-op
	err = EnsureEmbeddingDim(context.Background(), pool, 0)
	if err != nil {
		t.Fatalf("EnsureEmbeddingDim(0): %v", err)
	}
}
