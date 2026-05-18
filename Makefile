# Ivy Build Configuration

BINARY_VINE   := bin/vine
BINARY_LEAF   := bin/leaf
BINARY_IVY    := bin/ivy
GO            := go
GOFLAGS       := -trimpath -ldflags="-s -w"
MAIN_VINE     := ./cmd/vine
MAIN_LEAF     := ./cmd/leaf
MAIN_IVY      := ./cmd/ivy
PROTO_DIR     := proto
PROTO_OUT     := internal/ivyv1

.PHONY: all build build-vine build-leaf test test-e2e test-integration lint proto-gen migrate-up migrate-down docker-build docker-local-build docker-local docker-local-down docker-local-logs clean tidy

all: build

## build: Generate proto and build all binaries
build: proto-gen build-vine build-leaf build-ivy

## build-vine: Build the vine binary
build-vine:
	@echo "Building vine..."
	@mkdir -p bin
	$(GO) build $(GOFLAGS) -o $(BINARY_VINE) $(MAIN_VINE)

## build-leaf: Build the leaf binary
build-leaf:
	@echo "Building leaf..."
	@mkdir -p bin
	$(GO) build $(GOFLAGS) -o $(BINARY_LEAF) $(MAIN_LEAF)

## build-ivy: Build the ivy CLI binary
build-ivy:
	@echo "Building ivy CLI..."
	@mkdir -p bin
	$(GO) build $(GOFLAGS) -o $(BINARY_IVY) $(MAIN_IVY)

## test: Run all tests
test:
	$(GO) test -race -count=1 ./...

## test-e2e: Run full e2e tests (requires Docker + PostgreSQL + ~4GB RAM)
test-e2e:
	IVY_PIPELINE_TESTS=1 IVY_EMBEDDING_TESTS=1 $(GO) test -race -count=1 ./internal/ivyv1 ./internal/leaf/... ./internal/vine/...

## test-integration: Run integration tests (requires Docker)
test-integration:
	$(GO) test -race -tags=integration -count=1 ./...

## lint: Run golangci-lint
lint:
	golangci-lint run ./...

## proto-gen: Generate Go code from protobuf definitions
proto-gen:
	@echo "Generating protobuf code..."
	buf generate
	@echo "Done."

## migrate-up: Run database migrations up
migrate-up:
	$(GO) run github.com/pressly/goose/v3/cmd/goose@latest -dir migrations postgres "$(DB_URL)" up

## migrate-down: Run database migrations down
migrate-down:
	$(GO) run github.com/pressly/goose/v3/cmd/goose@latest -dir migrations postgres "$(DB_URL)" down

## migrate-create: Create a new migration file (usage: make migrate-create name=xxx)
migrate-create:
	$(GO) run github.com/pressly/goose/v3/cmd/goose@latest -dir migrations create $(name) sql

## docker-build: Build all Docker images (agent-sandbox + pipeline)
docker-build:
	@echo "Building agent-sandbox image..."
	docker build -f deploy/docker/agent-sandbox.Dockerfile -t ivy-agent-sandbox:latest .
	@echo "Building pipeline-sandbox images..."
	docker compose -f deploy/docker/docker-compose.pipeline-sandbox.yaml build

## docker-local-build: Build images for local testing (vine + leaf + sandbox images)
docker-local-build: docker-build
	@echo "Building vine image..."
	docker build -f deploy/docker/vine.Dockerfile -t ivy-vine:latest .
	@echo "Building leaf image..."
	docker build -f deploy/docker/leaf.Dockerfile -t ivy-leaf:latest .
	@echo "All local images built."

## docker-local: Start the full local testing stack
docker-local:
	docker compose --env-file .env -f deploy/docker/docker-compose.local.yaml up
	@echo ""
	@echo "Ivy local stack is running."
	@echo "  Vine gRPC:  localhost:50051"
	@echo "  Vine HTTP:  localhost:8080"
	@echo "  PostgreSQL: localhost:5432"
	@echo ""
	@echo "Check status: make docker-local-logs"
	@echo "Stop:         make docker-local-down"

## docker-local-down: Stop the local testing stack
docker-local-down:
	docker compose --env-file .env -f deploy/docker/docker-compose.local.yaml down

## clean: Remove build artifacts
clean:
	rm -rf bin/

## tidy: Run go mod tidy
tidy:
	$(GO) mod tidy

## help: Show this help
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^## ' Makefile | sed 's/## /  /'
