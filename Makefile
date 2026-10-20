.PHONY: help build run test vet lint docker-build docker-run clean

BINARY_NAME ?= neuro-occultist-bot

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "%-16s %s\n", $$1, $$2}'

build: ## Compile the bot into ./bin
	@mkdir -p bin
	go build -o bin/$(BINARY_NAME) ./cmd/bot

run: ## Run locally (loads .env / .env.local). Use BOT_MODE=polling.
	go run ./cmd/bot

test: ## Run unit tests
	go test ./...

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

docker-build: ## Build the container image
	docker build -t neuro-occultist-bot:local .

docker-run: ## Run the container with env from .env
	docker run --rm -p 8080:8080 --env-file .env neuro-occultist-bot:local

clean: ## Remove build artifacts
	rm -rf bin/ coverage.txt
