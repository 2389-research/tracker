# ABOUTME: Build, test, and quality gate targets for the tracker project.
# ABOUTME: Provides build targets, quality enforcement, and release helpers.

.PHONY: build test test-race test-short lint fmt fmt-check vet coverage \
        doctor complexity complexity-report ci install clean setup-hooks \
        tools-jail-check

GOCACHE ?= $(CURDIR)/.gocache
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  = -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
COVERAGE_THRESHOLD ?= 80

# Complexity thresholds
CYCLO_MAX     ?= 8
COGNITIVE_MAX ?= 8
FILE_MAX_LINES ?= 500
GOCYCLO_VERSION ?= v0.6.0
GOGNIT_VERSION  ?= v1.2.1
GOCYCLO := go run github.com/fzipp/gocyclo/cmd/gocyclo@$(GOCYCLO_VERSION)
GOGNIT  := go run github.com/uudashr/gocognit/cmd/gocognit@$(GOGNIT_VERSION)
QUALITY_BASELINE_DIR ?= .quality
CYCLO_BASELINE     ?= $(QUALITY_BASELINE_DIR)/gocyclo.baseline
COGNITIVE_BASELINE ?= $(QUALITY_BASELINE_DIR)/gocognit.baseline
FILE_SIZE_BASELINE ?= $(QUALITY_BASELINE_DIR)/file-size.baseline

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
	@FILES=$$(find . -name '*.go' \
		-not -path './.claude/*' \
		-not -path './.scratch/*' \
		-not -path './.gocache/*' \
		-not -path './.worktrees/*'); \
	if [ -n "$$FILES" ]; then gofmt -w $$FILES; fi

fmt-check:
	@FILES=$$(find . -name '*.go' \
		-not -path './.claude/*' \
		-not -path './.scratch/*' \
		-not -path './.gocache/*' \
		-not -path './.worktrees/*'); \
	NEEDS=$$(if [ -n "$$FILES" ]; then gofmt -l $$FILES; fi); \
	test -z "$$NEEDS" || { echo "gofmt: files need formatting:"; echo "$$NEEDS"; exit 1; }

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
	TMPDIR=$$(mktemp -d); \
	trap 'rm -rf "$$TMPDIR"' EXIT; \
	echo "--- Cyclomatic complexity (max $(CYCLO_MAX)) ---"; \
	($(GOCYCLO) -over $(CYCLO_MAX) . 2>&1 || true) | grep -v '_test.go' | grep -v 'cmd/tracker-conformance/' | grep -v '^exit status ' | sort > "$$TMPDIR/gocyclo"; \
	if diff -u "$(CYCLO_BASELINE)" "$$TMPDIR/gocyclo"; then \
		echo "OK: cyclomatic complexity matches baseline"; \
	else \
		echo "FAIL: cyclomatic complexity differs from $(CYCLO_BASELINE)"; \
		FAIL=1; \
	fi; \
	echo ""; \
	echo "--- Cognitive complexity (max $(COGNITIVE_MAX)) ---"; \
	($(GOGNIT) -over $(COGNITIVE_MAX) . 2>&1 || true) | grep -v '_test.go' | grep -v 'cmd/tracker-conformance/' | grep -v '^exit status ' | sort > "$$TMPDIR/gocognit"; \
	if diff -u "$(COGNITIVE_BASELINE)" "$$TMPDIR/gocognit"; then \
		echo "OK: cognitive complexity matches baseline"; \
	else \
		echo "FAIL: cognitive complexity differs from $(COGNITIVE_BASELINE)"; \
		FAIL=1; \
	fi; \
	echo ""; \
	echo "--- File size (max $(FILE_MAX_LINES) lines, excluding tests) ---"; \
	for f in $$(find . -name '*.go' -not -name '*_test.go' \
		-not -path './vendor/*' \
		-not -path './cmd/tracker-conformance/*' \
		-not -path './.scratch/*' \
		-not -path './.gocache/*' \
		-not -path './.worktrees/*'); do \
		LINES=$$(wc -l < "$$f" | tr -d ' '); \
		if [ "$$LINES" -gt $(FILE_MAX_LINES) ]; then \
			printf "  %6d  %s\n" "$$LINES" "$$f"; \
		fi; \
	done | sort -k2 > "$$TMPDIR/file-size"; \
	if diff -u "$(FILE_SIZE_BASELINE)" "$$TMPDIR/file-size"; then \
		echo "OK: file size matches baseline"; \
	else \
		echo "FAIL: file size differs from $(FILE_SIZE_BASELINE)"; \
		FAIL=1; \
	fi; \
	if [ "$$FAIL" -gt 0 ]; then exit 1; fi

complexity-report:
	@echo "═══ Complexity Report ═══"
	@echo ""
	@echo "--- Top 10 cyclomatic complexity (production code) ---"
	@($(GOCYCLO) -top 10 . 2>&1 || true) | grep -v '_test.go' | grep -v 'cmd/tracker-conformance/' | grep -v '^exit status ' | head -10
	@echo ""
	@echo "--- Top 10 cognitive complexity (production code) ---"
	@($(GOGNIT) -top 10 . 2>&1 || true) | grep -v '_test.go' | grep -v 'cmd/tracker-conformance/' | grep -v '^exit status ' | head -10
	@echo ""
	@echo "--- Files over $(FILE_MAX_LINES) lines (production code) ---"
	@for f in $$(find . -name '*.go' -not -name '*_test.go' -not -path './vendor/*' -not -path './cmd/tracker-conformance/*' -not -path './.scratch/*' -not -path './.gocache/*' -not -path './.worktrees/*'); do \
		LINES=$$(wc -l < "$$f" | tr -d ' '); \
		if [ "$$LINES" -gt $(FILE_MAX_LINES) ]; then \
			printf "  %6d  %s\n" "$$LINES" "$$f"; \
		fi; \
	done | sort -rn
	@echo ""
	@echo "--- Summary ---"
	@echo "  Cyclomatic > $(CYCLO_MAX):  $$(( $(GOCYCLO) -over $(CYCLO_MAX) . 2>&1 || true ) | grep -v '_test.go' | grep -v 'cmd/tracker-conformance/' | grep -v '^exit status ' | wc -l | tr -d ' ') functions"
	@echo "  Cognitive > $(COGNITIVE_MAX): $$(( $(GOGNIT) -over $(COGNITIVE_MAX) . 2>&1 || true ) | grep -v '_test.go' | grep -v 'cmd/tracker-conformance/' | grep -v '^exit status ' | wc -l | tr -d ' ') functions"
	@echo "  Files > $(FILE_MAX_LINES) LOC:  $$(find . -name '*.go' -not -name '*_test.go' -not -path './vendor/*' -not -path './cmd/tracker-conformance/*' -not -path './.scratch/*' -not -path './.gocache/*' -not -path './.worktrees/*' -exec sh -c 'test $$(wc -l < "$$1" | tr -d " ") -gt $(FILE_MAX_LINES) && echo 1' _ {} \; | wc -l | tr -d ' ') files"

# ─── Lint ────────────────────────────────────────────────

# DIPPIN_VERSION is derived from go.mod so the local `dippin` binary and
# the go module always match. This avoids "unrecognized field" failures
# when a contributor's PATH binary lags behind the dep bump.
DIPPIN_VERSION := $(shell awk '/github.com\/2389-research\/dippin-lang/ {print $$2}' go.mod)
DIPPIN := go run github.com/2389-research/dippin-lang/cmd/dippin@$(DIPPIN_VERSION)

lint:
	@FAIL=0; \
	for f in examples/*.dip; do \
		ERRORS=$$($(DIPPIN) check "$$f" 2>&1 | python3 -c "import sys,json; d=json.loads(sys.stdin.read()); print(d.get('errors',0))" 2>/dev/null || echo "0"); \
		if [ "$$ERRORS" -gt 0 ]; then \
			echo "FAIL: $$f has $$ERRORS errors"; \
			FAIL=1; \
		fi; \
	done; \
	if [ "$$FAIL" -gt 0 ]; then exit 1; fi
	@echo "All .dip files pass lint (via $(DIPPIN_VERSION))"

doctor:
	@FAIL=0; \
	for f in examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip examples/manager_loop_demo.dip; do \
		GRADE=$$($(DIPPIN) doctor "$$f" 2>&1 | grep 'Grade' | sed 's/.*Grade: //' | sed 's/  .*//'); \
		SCORE=$$($(DIPPIN) doctor "$$f" 2>&1 | grep 'Score' | sed 's/.*Score: //' | sed 's/\/100//'); \
		printf "%-50s %s  %s/100\n" "$$(basename $$f)" "$$GRADE" "$$SCORE"; \
		if [ "$$GRADE" != "A" ]; then \
			FAIL=1; \
		fi; \
	done; \
	if [ "$$FAIL" -gt 0 ]; then echo "FAIL: core pipelines must be grade A"; exit 1; fi
	@echo "All core pipelines grade A (via $(DIPPIN_VERSION))"

# ─── Agent-tool jail lint ────────────────────────────────

# tools-jail-check flags direct filesystem-mutation / subprocess calls in
# agent/tools/ that bypass the ExecutionEnvironment seam guarding the
# writable_paths jail (#283, refs #275/#272). It watches os, os/exec, io/ioutil,
# and syscall. The single legal exception — an env==nil fallback — must carry a
# //jail:allow-unjailed-fallback marker on its function. See
# docs/architecture/agent-tool-jail-checklist.md.
tools-jail-check:
	@GOCACHE=$(GOCACHE) go run ./tools/jailcheck agent/tools

# ─── CI (all gates in sequence) ──────────────────────────

ci: fmt-check vet build test-short test-race coverage lint doctor complexity tools-jail-check
	@echo ""
	@echo "═══ All CI gates passed ═══"

# ─── Setup ───────────────────────────────────────────────

setup-hooks:
	ln -sf ../../.pre-commit .git/hooks/pre-commit
	@echo "Pre-commit hook installed"
