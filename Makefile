# Ivy Build Configuration

BINARY_VINE   := bin/vine
BINARY_LEAF   := bin/leaf
GO            := go
GOFLAGS       := -trimpath -ldflags="-s -w"
MAIN_VINE     := ./cmd/vine
MAIN_LEAF     := ./cmd/leaf
PROTO_DIR     := proto
PROTO_OUT     := internal/ivyv1

.PHONY: all build build-vine build-leaf test lint proto-gen migrate-up migrate-down docker-build clean tidy

all: build

## build: Generate proto and build all binaries
build: proto-gen build-vine build-leaf

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

## test: Run all tests
test:
	$(GO) test -race -count=1 ./...

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

## docker-build: Build all Docker images
docker-build:
	@echo "Building agent-sandbox image..."
	docker build -f deploy/docker/agent-sandbox.Dockerfile -t ivy-agent-sandbox:latest .
	@echo "Building pipeline-sandbox images..."
	docker compose -f deploy/docker/pipeline-sandbox-compose.yml build

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
