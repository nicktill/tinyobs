.PHONY: build server example clean test docker-build docker-run docker-compose-up docker-compose-down docker-clean help

# Build all binaries
build:
	go build -o bin/tinyobs-server cmd/server/main.go
	go build -o bin/tinyobs-example cmd/example/main.go

# Start the ingest server
server:
	go run cmd/server/main.go

# Start the example application
example:
	go run cmd/example/main.go

# Run both server and example in background
demo: server example

# Clean build artifacts
clean:
	rm -rf bin/

# Run tests
test:
	go test ./...

# Install dependencies
deps:
	go mod tidy
	go mod download

# Docker commands
docker-up:
	docker-compose up -d --build

docker-down:
	docker-compose --profile example down --remove-orphans

docker-logs:
	docker-compose logs -f

docker-demo:
	docker-compose --profile example up -d --build

# Show help
help:
	@echo "TinyObs - Lightweight Observability SDK"
	@echo ""
	@echo "Available commands:"
	@echo "  Development:"
	@echo "    make server    - Start the ingest server (port 8080)"
	@echo "    make example   - Start the example app (port 3000)"
	@echo "    make demo      - Start both server and example"
	@echo "    make build     - Build all binaries"
	@echo "    make test      - Run tests"
	@echo "    make clean     - Clean build artifacts"
	@echo "    make deps      - Install dependencies"
	@echo ""
	@echo "  Docker:"
	@echo "    make docker-up    - Start server only"
	@echo "    make docker-demo  - Start server + example app"
	@echo "    make docker-down  - Stop all services"
	@echo "    make docker-logs  - View logs"
	@echo ""
	@echo "Quick start (local):"
	@echo "  1. make server    (in terminal 1)"
	@echo "  2. make example   (in terminal 2)"
	@echo "  3. Visit http://localhost:8080 for dashboard"
	@echo ""
	@echo "Quick start (Docker):"
	@echo "  1. make docker-up    - Server only"
	@echo "  2. make docker-demo  - Server + example app"
	@echo "  3. Visit http://localhost:8080 for dashboard"


