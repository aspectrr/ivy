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
