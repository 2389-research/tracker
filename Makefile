# ABOUTME: Build and test targets for the mammoth-lite project.
# ABOUTME: Provides build targets for both mammoth and conformance binaries.

.PHONY: build test clean

build:
	go build -o bin/conformance ./cmd/conformance

test:
	go test ./...

clean:
	rm -rf bin/
