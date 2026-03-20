# ABOUTME: Build and test targets for the tracker project.
# ABOUTME: Provides build targets for both tracker and tracker-conformance binaries.

.PHONY: build test lint clean install

GOCACHE ?= $(CURDIR)/.gocache

build:
	mkdir -p bin
	GOCACHE=$(GOCACHE) go build -o bin/tracker ./cmd/tracker
	GOCACHE=$(GOCACHE) go build -o bin/tracker-conformance ./cmd/tracker-conformance

test:
	GOCACHE=$(GOCACHE) go test ./...

lint:
	@command -v dippin >/dev/null 2>&1 || { echo "dippin CLI not found; skipping .dip lint"; exit 0; }
	@find examples -name '*.dip' -exec sh -c 'echo "checking {}..." && dippin check "{}"' \;

INSTALL_DIR ?= $(if $(XDG_BIN_HOME),$(XDG_BIN_HOME),$(HOME)/.local/bin)

install: build
	mkdir -p "$(INSTALL_DIR)"
	cp bin/tracker "$(INSTALL_DIR)/tracker"
	@echo "Installed tracker to $(INSTALL_DIR)/tracker"

clean:
	rm -rf bin/
