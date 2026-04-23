APP_NAME := infrastructure-intelligence-service
DOCKER_IMAGE := infrastructure-intelligence-service:latest
DATABASE_URL := postgres://postgres:postgres@localhost:5432/augur_service?sslmode=disable

.PHONY: help build run test test-integration lint fmt clean docker-build

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

build: ## Build the binary
	go build -o bin/$(APP_NAME) ./cmd/server

run: ## Run locally
	go run ./cmd/server

test: ## Run unit tests
	go test ./internal/... ./pkg/... -v -count=1

test-integration: ## Run integration tests
	go test ./test/integration/... -tags=integration -v -count=1

lint: ## Run linter
	golangci-lint run ./...

fmt: ## Format code
	gofmt -s -w .

clean: ## Clean build artifacts
	rm -rf bin/

docker-build: ## Build Docker image
	docker build -t $(DOCKER_IMAGE) -f Dockerfile ..

tidy: ## Tidy go modules
	go mod tidy
