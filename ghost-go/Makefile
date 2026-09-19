# ghost-go build & test Makefile
# Requires: Go 1.25+

BUILD_DIR := ./build

.PHONY: all build vet test test-race test-coverage clean help

all: build

help:
	@echo "ghost-go targets:"
	@echo "  build          Compile all packages"
	@echo "  vet            Run go vet"
	@echo "  test           Run all tests"
	@echo "  test-race      Run all tests with the race detector"
	@echo "  test-coverage  Run tests and write coverage.html"
	@echo "  clean          Remove build artifacts"

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

test-race:
	go test -race ./...

test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

clean:
	rm -rf $(BUILD_DIR) coverage.out coverage.html
