.PHONY: all build clean run-solver run-relayer test fmt lint

# Build variables
BINARY_DIR=bin
SOLVER_BINARY=$(BINARY_DIR)/solver
RELAYER_BINARY=$(BINARY_DIR)/relayer

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt

all: clean build

# Build both services
build: build-solver build-relayer

build-solver:
	@echo "Building solver..."
	@mkdir -p $(BINARY_DIR)
	$(GOBUILD) -o $(SOLVER_BINARY) ./cmd/solver

build-relayer:
	@echo "Building relayer..."
	@mkdir -p $(BINARY_DIR)
	$(GOBUILD) -o $(RELAYER_BINARY) ./cmd/relayer

# Build with optimizations
build-optimized:
	@echo "Building with optimizations..."
	@mkdir -p $(BINARY_DIR)
	$(GOBUILD) -ldflags="-s -w" -o $(SOLVER_BINARY) ./cmd/solver
	$(GOBUILD) -ldflags="-s -w" -o $(RELAYER_BINARY) ./cmd/relayer

# Clean build artifacts
clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	rm -rf $(BINARY_DIR)

# Run services
run-solver:
	@echo "Running solver..."
	$(GOCMD) run ./cmd/solver

run-relayer:
	@echo "Running relayer..."
	$(GOCMD) run ./cmd/relayer

# Development commands
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

fmt:
	@echo "Formatting code..."
	$(GOFMT) ./...

lint:
	@echo "Running linter..."
	golangci-lint run

# Dependency management
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download

tidy:
	@echo "Tidying dependencies..."
	$(GOMOD) tidy

# Docker commands (optional)
docker-build-solver:
	docker build -t tachyon-solver -f Dockerfile.solver .

docker-build-relayer:
	docker build -t tachyon-relayer -f Dockerfile.relayer .

# E2E Testing
test-e2e:
	@echo "Running E2E tests..."
	cd test/e2e && ./run_test.sh

test-e2e-setup:
	@echo "Setting up E2E test environment..."
	cd test/e2e && cp .env.test.example .env.test
	@echo "✅ Created .env.test - please edit with your configuration"

# Help
help:
	@echo "Available targets:"
	@echo "  make build              - Build both services"
	@echo "  make build-solver       - Build solver service"
	@echo "  make build-relayer      - Build relayer service"
	@echo "  make build-optimized    - Build with optimizations"
	@echo "  make clean              - Clean build artifacts"
	@echo "  make run-solver         - Run solver service"
	@echo "  make run-relayer        - Run relayer service"
	@echo "  make test               - Run tests"
	@echo "  make test-e2e           - Run E2E tests"
	@echo "  make test-e2e-setup     - Setup E2E test environment"
	@echo "  make fmt                - Format code"
	@echo "  make lint               - Run linter"
	@echo "  make deps               - Download dependencies"
	@echo "  make tidy               - Tidy dependencies"
