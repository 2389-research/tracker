# ABOUTME: Build, test, and quality gate targets for the tracker project.
# ABOUTME: Provides build targets, quality enforcement, and release helpers.

.PHONY: build test test-race test-short lint fmt fmt-check vet coverage \
        doctor ci install clean setup-hooks

GOCACHE ?= $(CURDIR)/.gocache
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  = -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
COVERAGE_THRESHOLD ?= 80

# ─── Build ───────────────────────────────────────────────

build:
	mkdir -p bin
	GOCACHE=$(GOCACHE) go build -ldflags "$(LDFLAGS)" -o bin/tracker ./cmd/tracker
	GOCACHE=$(GOCACHE) go build -o bin/tracker-conformance ./cmd/tracker-conformance

INSTALL_DIR ?= $(if $(XDG_BIN_HOME),$(XDG_BIN_HOME),$(HOME)/.local/bin)

install: build
	mkdir -p "$(INSTALL_DIR)"
	cp bin/tracker "$(INSTALL_DIR)/tracker"
	@echo "Installed tracker to $(INSTALL_DIR)/tracker"

clean:
	rm -rf bin/ .gocache/

# ─── Quality Gates ───────────────────────────────────────

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || { echo "gofmt: files need formatting:"; gofmt -l .; exit 1; }

vet:
	go vet ./...

test:
	GOCACHE=$(GOCACHE) go test ./...

test-short:
	GOCACHE=$(GOCACHE) go test ./... -short

test-race:
	GOCACHE=$(GOCACHE) go test -race -short ./pipeline/... ./tui/... ./agent/...

coverage:
	@go test ./pipeline/... -short -coverprofile=coverage.out > /dev/null 2>&1
	@TOTAL=$$(go tool cover -func=coverage.out | tail -1 | awk '{print $$NF}' | tr -d '%'); \
	echo "Pipeline coverage: $${TOTAL}%"; \
	if [ $$(echo "$${TOTAL} < $(COVERAGE_THRESHOLD)" | bc -l) -eq 1 ]; then \
		echo "FAIL: coverage $${TOTAL}% < $(COVERAGE_THRESHOLD)% threshold"; \
		exit 1; \
	fi
	@rm -f coverage.out

lint:
	@command -v dippin >/dev/null 2>&1 || { echo "dippin CLI not found; skipping .dip lint"; exit 0; }
	@FAIL=0; \
	for f in examples/*.dip; do \
		ERRORS=$$(dippin check "$$f" 2>&1 | python3 -c "import sys,json; d=json.loads(sys.stdin.read()); print(d.get('errors',0))" 2>/dev/null || echo "0"); \
		if [ "$$ERRORS" -gt 0 ]; then \
			echo "FAIL: $$f has $$ERRORS errors"; \
			FAIL=1; \
		fi; \
	done; \
	if [ "$$FAIL" -gt 0 ]; then exit 1; fi
	@echo "All .dip files pass lint"

doctor:
	@command -v dippin >/dev/null 2>&1 || { echo "dippin CLI not found; skipping doctor"; exit 0; }
	@FAIL=0; \
	for f in examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip; do \
		GRADE=$$(dippin doctor "$$f" 2>&1 | grep 'Grade' | sed 's/.*Grade: //' | sed 's/  .*//'); \
		SCORE=$$(dippin doctor "$$f" 2>&1 | grep 'Score' | sed 's/.*Score: //' | sed 's/\/100//'); \
		printf "%-50s %s  %s/100\n" "$$(basename $$f)" "$$GRADE" "$$SCORE"; \
		if [ "$$GRADE" != "A" ]; then \
			FAIL=1; \
		fi; \
	done; \
	if [ "$$FAIL" -gt 0 ]; then echo "FAIL: core pipelines must be grade A"; exit 1; fi
	@echo "All core pipelines grade A"

# ─── CI (all gates in sequence) ──────────────────────────

ci: fmt-check vet build test-short test-race coverage lint doctor
	@echo ""
	@echo "═══ All CI gates passed ═══"

# ─── Setup ───────────────────────────────────────────────

setup-hooks:
	ln -sf ../../.pre-commit .git/hooks/pre-commit
	@echo "Pre-commit hook installed"
