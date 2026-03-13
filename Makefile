.PHONY: help build run dev clean docker-up docker-down docker-logs colima-start colima-stop colima-status

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ===========================================
# Colima (Docker Engine for macOS)
# ===========================================

colima-start: ## Start Colima Docker engine
	@echo "Starting Colima..."
	@if command -v colima >/dev/null 2>&1; then \
		colima start --cpu 4 --memory 8 --disk 60; \
		echo "Colima started successfully"; \
	else \
		echo "Error: Colima is not installed. Install with: brew install colima"; \
		exit 1; \
	fi

colima-stop: ## Stop Colima Docker engine
	@echo "Stopping Colima..."
	@colima stop

colima-status: ## Check Colima status
	@colima status 2>/dev/null || echo "Colima is not running"

colima-restart: ## Restart Colima Docker engine
	@echo "Restarting Colima..."
	@colima restart

# ===========================================
# Development
# ===========================================

build-backend: ## Build backend binary
	@echo "Building backend..."
	@go build -o bin/server ./cmd/server

build-frontend: ## Build frontend
	@echo "Building frontend..."
	@cd frontend && npm run build

build: build-backend build-frontend ## Build all components

dev-backend: ## Run backend in development mode
	@echo "Running backend in development mode..."
	@go run ./cmd/server

dev-frontend: ## Run frontend in development mode
	@echo "Running frontend in development mode..."
	@cd frontend && npm run dev

dev: ## Run all in development mode
	@make -j2 dev-backend dev-frontend

clean: ## Clean build artifacts
	@echo "Cleaning..."
	@rm -rf bin
	@rm -rf frontend/dist
	@rm -rf frontend/node_modules

# ===========================================
# Docker
# ===========================================

_check-colima:
	@if ! colima status >/dev/null 2>&1; then \
		echo "Colima is not running. Starting..."; \
		$(MAKE) colima-start; \
	fi

docker-build: _check-colima ## Build Docker images
	@echo "Building Docker images..."
	@docker-compose build

docker-up: _check-colima ## Start Docker containers
	@echo "Starting Docker containers..."
	@echo "Using AWS config from ~/.aws"
	@docker-compose up -d

docker-down: ## Stop Docker containers
	@echo "Stopping Docker containers..."
	@docker-compose down

docker-logs: ## Show Docker logs
	@docker-compose logs -f

docker-ps: ## Show Docker container status
	@docker-compose ps

docker-clean: ## Clean Docker volumes
	@docker-compose down -v

docker-restart: docker-down docker-up ## Restart Docker containers

# ===========================================
# Database
# ===========================================

migrate-up: ## Run database migrations
	@echo "Running migrations..."
	@go run ./cmd/migrate up

migrate-down: ## Rollback database migrations
	@echo "Rolling back migrations..."
	@go run ./cmd/migrate down

# ===========================================
# Testing
# ===========================================

test: ## Run tests
	@echo "Running tests..."
	@go test ./... -v

test-coverage: ## Run tests with coverage
	@echo "Running tests with coverage..."
	@go test ./... -coverprofile=coverage.out
	@go tool cover -html=coverage.out

# ===========================================
# Setup & Init
# ===========================================

install-deps: ## Install dependencies
	@echo "Installing backend dependencies..."
	@go mod download
	@echo "Installing frontend dependencies..."
	@cd frontend && npm install

setup: install-deps ## Setup development environment
	@echo "Setting up development environment..."
	@cp .env.example .env
	@echo "Please edit .env with your configuration"

init-opensearch: ## Initialize OpenSearch default indices
	@echo "Initializing OpenSearch default indices..."
	@curl -X PUT "http://localhost:9200/knowledge_text_default" -H 'Content-Type: application/json' -d'{"settings":{"number_of_shards":3,"number_of_replicas":1},"mappings":{"properties":{"tenant_id":{"type":"keyword"},"knowledge_base_id":{"type":"keyword"},"document_id":{"type":"keyword"},"chunk_id":{"type":"keyword"},"content":{"type":"text"},"title":{"type":"text"},"doc_type":{"type":"keyword"},"metadata":{"type":"object","enabled":true}}}}' || true
	@curl -X PUT "http://localhost:9200/knowledge_vector_default" -H 'Content-Type: application/json' -d'{"settings":{"number_of_shards":3,"number_of_replicas":1},"mappings":{"properties":{"tenant_id":{"type":"keyword"},"knowledge_base_id":{"type":"keyword"},"document_id":{"type":"keyword"},"chunk_id":{"type":"keyword"},"title":{"type":"text"},"content":{"type":"text"},"embedding":{"type":"knn_vector","dimension":1024,"method":{"name":"hnsw","engine":"nmslib","space_type":"cosinesimil","parameters":{"ef_construction":256,"m":16}}}}}}' || true

init-neo4j: ## Initialize Neo4j constraints
	@echo "Initializing Neo4j..."
	@curl -X POST "http://localhost:7474/db/neo4j/tx/commit" -H 'Content-Type: application/json' -d'{"statements":[{"statement":"CREATE CONSTRAINT IF NOT EXISTS FOR (n:Document) REQUIRE n.id IS UNIQUE"}]}'

init-all: init-opensearch init-neo4j ## Initialize all services
	@echo "All services initialized"

# ===========================================
# Quick Start
# ===========================================

quick-start: colima-start docker-up ## Quick start: start Colima and Docker containers
	@echo "Services are starting..."
	@echo "Wait for services to be healthy, then run: make init-all"
	@echo "Backend API: http://localhost:8080"
	@echo "Frontend: http://localhost"
