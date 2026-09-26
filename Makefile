.PHONY: build run example test promql-compat docker-up docker-demo docker-down clean help

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/nicktill/tinyobs/pkg/server.Version=$(VERSION)

build: ## Build bin/tinyobs and the example app
	go build -trimpath -ldflags="$(LDFLAGS)" -o bin/tinyobs ./cmd/tinyobs
	go build -o bin/tinyobs-example ./cmd/example

run: ## Run TinyObs on http://localhost:8421
	go run ./cmd/tinyobs

example: ## Run the example app, which sends metrics to TinyObs
	go run ./cmd/example

test: ## Run the tests with the race detector
	go test -race ./...

promql-compat: ## Check the query engine against Prometheus's PromQL tests
	sh scripts/fetch-promql-tests.sh
	go test ./pkg/promql -run TestPrometheusConformance -v

docker-up: ## Run TinyObs in Docker
	docker compose up -d --build

docker-demo: ## Run TinyObs and the example app in Docker
	docker compose --profile example up -d --build

docker-down: ## Stop the Docker services
	docker compose --profile example down

clean:
	rm -rf bin/

help:
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  make %-14s %s\n", $$1, $$2}'
