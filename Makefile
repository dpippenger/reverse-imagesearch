.PHONY: all build clean install test test-race coverage run deps

BINARY_NAME=imgsearch
BUILD_DIR=build
GO=go

# Build flags
LDFLAGS=-s -w
GCFLAGS=

# Detect OS and architecture
GOOS?=$(shell go env GOOS)
GOARCH?=$(shell go env GOARCH)

all: build

# Download dependencies
deps:
	$(GO) mod tidy
	$(GO) mod download

# Build the application
build: deps
	$(GO) build -ldflags="$(LDFLAGS)" -o $(BINARY_NAME) ./cmd/imgsearch

# Build with debug symbols
build-debug: deps
	$(GO) build -o $(BINARY_NAME) ./cmd/imgsearch

# Build for multiple platforms
build-all: deps
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/imgsearch
	GOOS=linux GOARCH=arm64 $(GO) build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/imgsearch
	GOOS=darwin GOARCH=amd64 $(GO) build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/imgsearch
	GOOS=darwin GOARCH=arm64 $(GO) build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/imgsearch
	GOOS=windows GOARCH=amd64 $(GO) build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/imgsearch

# Install to GOPATH/bin
install: deps
	$(GO) install -ldflags="$(LDFLAGS)" ./cmd/imgsearch

# Clean build artifacts
clean:
	$(GO) clean
	rm -f $(BINARY_NAME)
	rm -rf $(BUILD_DIR)

# Run tests
test:
	$(GO) test -v ./...

# Run tests with race detector
test-race:
	$(GO) test -race ./...

# Generate coverage report
coverage:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Run the application (requires SOURCE and optionally DIR)
run: build
	./$(BINARY_NAME) -source $(SOURCE) -dir $(or $(DIR),.)

# Show help
help:
	@echo "Available targets:"
	@echo "  make build       - Build the application"
	@echo "  make build-debug - Build with debug symbols"
	@echo "  make build-all   - Build for all platforms (linux, darwin, windows)"
	@echo "  make install     - Install to GOPATH/bin"
	@echo "  make clean       - Remove build artifacts"
	@echo "  make deps        - Download dependencies"
	@echo "  make test        - Run tests"
	@echo "  make test-race   - Run tests with race detector"
	@echo "  make coverage    - Generate HTML coverage report"
	@echo "  make run SOURCE=<image> [DIR=<path>] - Build and run"
	@echo "  make help        - Show this help"
