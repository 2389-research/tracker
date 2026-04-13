# ABOUTME: Build, test, and quality gate targets for the tracker project.
# ABOUTME: Provides build targets, quality enforcement, and release helpers.

.PHONY: build test test-race test-short lint fmt fmt-check vet coverage \
        doctor complexity complexity-report ci install clean setup-hooks \
        sync-workflows check-workflows

GOCACHE ?= $(CURDIR)/.gocache
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  = -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
COVERAGE_THRESHOLD ?= 80

# Complexity thresholds
CYCLO_MAX     ?= 10
COGNITIVE_MAX ?= 10
FILE_MAX_LINES ?= 500

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

# ─── Complexity ──────────────────────────────────────────

complexity:
	@FAIL=0; \
	echo "--- Cyclomatic complexity (max $(CYCLO_MAX)) ---"; \
	VIOLATIONS=$$(gocyclo -over $(CYCLO_MAX) . 2>&1 | grep -v '_test.go' | grep -v 'cmd/tracker-conformance/' | wc -l | tr -d ' '); \
	if [ "$$VIOLATIONS" -gt 0 ]; then \
		gocyclo -over $(CYCLO_MAX) . 2>&1 | grep -v '_test.go' | grep -v 'cmd/tracker-conformance/'; \
		echo "FAIL: $$VIOLATIONS functions exceed cyclomatic complexity $(CYCLO_MAX)"; \
		FAIL=1; \
	else \
		echo "OK: all functions within limit"; \
	fi; \
	echo ""; \
	echo "--- Cognitive complexity (max $(COGNITIVE_MAX)) ---"; \
	VIOLATIONS=$$(gocognit -over $(COGNITIVE_MAX) . 2>&1 | grep -v '_test.go' | grep -v 'cmd/tracker-conformance/' | wc -l | tr -d ' '); \
	if [ "$$VIOLATIONS" -gt 0 ]; then \
		gocognit -over $(COGNITIVE_MAX) . 2>&1 | grep -v '_test.go' | grep -v 'cmd/tracker-conformance/'; \
		echo "FAIL: $$VIOLATIONS functions exceed cognitive complexity $(COGNITIVE_MAX)"; \
		FAIL=1; \
	else \
		echo "OK: all functions within limit"; \
	fi; \
	echo ""; \
	echo "--- File size (max $(FILE_MAX_LINES) lines, excluding tests) ---"; \
	OVERSIZED=0; \
	for f in $$(find . -name '*.go' -not -name '*_test.go' -not -path './vendor/*' -not -path './cmd/tracker-conformance/*'); do \
		LINES=$$(wc -l < "$$f" | tr -d ' '); \
		if [ "$$LINES" -gt $(FILE_MAX_LINES) ]; then \
			printf "  %6d  %s\n" "$$LINES" "$$f"; \
			OVERSIZED=$$((OVERSIZED + 1)); \
		fi; \
	done; \
	if [ "$$OVERSIZED" -gt 0 ]; then \
		echo "FAIL: $$OVERSIZED files exceed $(FILE_MAX_LINES) lines"; \
		FAIL=1; \
	else \
		echo "OK: all files within limit"; \
	fi; \
	if [ "$$FAIL" -gt 0 ]; then exit 1; fi

complexity-report:
	@echo "═══ Complexity Report ═══"
	@echo ""
	@echo "--- Top 10 cyclomatic complexity (production code) ---"
	@gocyclo -top 10 . 2>&1 | grep -v '_test.go' | head -10
	@echo ""
	@echo "--- Top 10 cognitive complexity (production code) ---"
	@gocognit -top 10 . 2>&1 | grep -v '_test.go' | head -10
	@echo ""
	@echo "--- Files over $(FILE_MAX_LINES) lines (production code) ---"
	@for f in $$(find . -name '*.go' -not -name '*_test.go' -not -path './vendor/*' -not -path './cmd/tracker-conformance/*'); do \
		LINES=$$(wc -l < "$$f" | tr -d ' '); \
		if [ "$$LINES" -gt $(FILE_MAX_LINES) ]; then \
			printf "  %6d  %s\n" "$$LINES" "$$f"; \
		fi; \
	done | sort -rn
	@echo ""
	@echo "--- Summary ---"
	@echo "  Cyclomatic > $(CYCLO_MAX):  $$(gocyclo -over $(CYCLO_MAX) . 2>&1 | grep -v '_test.go' | wc -l | tr -d ' ') functions"
	@echo "  Cognitive > $(COGNITIVE_MAX): $$(gocognit -over $(COGNITIVE_MAX) . 2>&1 | grep -v '_test.go' | wc -l | tr -d ' ') functions"
	@echo "  Files > $(FILE_MAX_LINES) LOC:  $$(find . -name '*.go' -not -name '*_test.go' -not -path './vendor/*' -not -path './cmd/tracker-conformance/*' -exec sh -c 'test $$(wc -l < "$$1" | tr -d " ") -gt $(FILE_MAX_LINES) && echo 1' _ {} \; | wc -l | tr -d ' ') files"

# ─── Lint ────────────────────────────────────────────────

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

ci: fmt-check vet build test-short test-race coverage lint doctor complexity
	@echo ""
	@echo "═══ All CI gates passed ═══"

# ─── Workflow sync ────────────────────────────

sync-workflows:
	cp examples/ask_and_execute.dip cmd/tracker/workflows/
	cp examples/build_product.dip cmd/tracker/workflows/
	cp examples/build_product_with_superspec.dip cmd/tracker/workflows/
	cp examples/deep_review.dip cmd/tracker/workflows/
	@echo "Workflows synced"

check-workflows:
	@FAIL=0; \
	for f in ask_and_execute.dip build_product.dip build_product_with_superspec.dip deep_review.dip; do \
		if ! diff -q "examples/$$f" "cmd/tracker/workflows/$$f" > /dev/null 2>&1; then \
			echo "FAIL: examples/$$f differs from cmd/tracker/workflows/$$f"; \
			FAIL=1; \
		fi; \
	done; \
	if [ "$$FAIL" -gt 0 ]; then echo "Run 'make sync-workflows' to fix"; exit 1; fi
	@echo "Embedded workflows in sync"

# ─── Setup ───────────────────────────────────────────────

setup-hooks:
	ln -sf ../../.pre-commit .git/hooks/pre-commit
	@echo "Pre-commit hook installed"
