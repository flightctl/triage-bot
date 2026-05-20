IMAGE_NAME := triage-bot
TAG        := latest
REGISTRY   ?=
CONTAINER_TOOL := $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)

.PHONY: build push unit-test fmt lint docs-lint helm-lint tidy help

help:
	@echo "Available targets:"
	@echo "  build      - Build the container image"
	@echo "  push       - Push to registry (REGISTRY=quay.io/myorg)"
	@echo "  unit-test  - Run unit tests with race detector"
	@echo "  fmt        - Auto-format code"
	@echo "  lint       - Run golangci-lint"
	@echo "  docs-lint  - Lint and fix markdown files"
	@echo "  helm-lint  - Lint Helm chart"
	@echo "  tidy       - Run go mod tidy"

build:
	@echo "Building $(IMAGE_NAME):$(TAG)..."
	$(CONTAINER_TOOL) build --no-cache --tag $(IMAGE_NAME):$(TAG) --file Dockerfile .

push:
ifndef REGISTRY
	$(error REGISTRY is required. Usage: make push REGISTRY=quay.io/myorg)
endif
	$(CONTAINER_TOOL) tag $(IMAGE_NAME):$(TAG) $(REGISTRY)/$(IMAGE_NAME):$(TAG)
	$(CONTAINER_TOOL) push $(REGISTRY)/$(IMAGE_NAME):$(TAG)

unit-test:
	@echo "Running unit tests..."
	go test -v -race ./...

fmt:
	@echo "Formatting code..."
	gofmt -w .
	gci write --section standard --section default --section "prefix(triage-bot)" .

lint:
	@echo "Running golangci-lint..."
	golangci-lint run ./...

docs-lint:
	@echo "Linting markdown files..."
	markdownlint-cli2 --fix "**/*.md" "!**/AGENTS.md" "!**/CLAUDE.md"

helm-lint:
	@echo "Linting Helm chart..."
	helm lint chart/triage-bot

tidy:
	@echo "Running go mod tidy..."
	go mod tidy
