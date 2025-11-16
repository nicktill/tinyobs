.PHONY: build server example clean test

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

# Show help
help:
	@echo "TinyObs - Lightweight Observability SDK"
	@echo ""
	@echo "Available commands:"
	@echo "  make server    - Start the ingest server (port 8080)"
	@echo "  make example   - Start the example app (port 3001)"
	@echo "  make demo      - Start both server and example"
	@echo "  make build     - Build all binaries"
	@echo "  make test      - Run tests"
	@echo "  make clean     - Clean build artifacts"
	@echo "  make deps      - Install dependencies"
	@echo ""
	@echo "Quick start:"
	@echo "  1. make server    (in terminal 1)"
	@echo "  2. make example   (in terminal 2)"
	@echo "  3. Visit http://localhost:8080 for dashboard"
	@echo "  4. Visit http://localhost:3001 for example app"


