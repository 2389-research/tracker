set -eu
REPORT=".ai/gates/phase1.txt"
PASS=true

echo "=== Phase 1 Quality Gates ===" > "$REPORT"

# QG-2: Static quality
if [ -f pyproject.toml ]; then
  echo "--- Lint ---" >> "$REPORT"
  uv run ruff check . >> "$REPORT" 2>&1 || PASS=false
  echo "--- Type check ---" >> "$REPORT"
  uv run pyright . >> "$REPORT" 2>&1 || PASS=false
elif [ -f go.mod ]; then
  echo "--- Vet ---" >> "$REPORT"
  go vet ./... >> "$REPORT" 2>&1 || PASS=false
fi

# QG-3: Test coverage
if [ -f go.mod ]; then
  echo "--- Tests ---" >> "$REPORT"
  go test ./... -coverprofile=.ai/gates/phase1-coverage.out >> "$REPORT" 2>&1 || PASS=false
  echo "--- Coverage ---" >> "$REPORT"
  go tool cover -func=.ai/gates/phase1-coverage.out | tail -1 >> "$REPORT"
elif [ -f pyproject.toml ]; then
  echo "--- Tests ---" >> "$REPORT"
  uv run pytest --cov --cov-report=term-missing >> "$REPORT" 2>&1 || PASS=false
fi

# QG-5: Complexity (if gocyclo or similar available)
if command -v gocyclo >/dev/null 2>&1 && [ -f go.mod ]; then
  echo "--- Complexity ---" >> "$REPORT"
  COMPLEX=$(gocyclo -over 10 . 2>/dev/null | wc -l | tr -d ' ')
  echo "Functions over cyclomatic 10: $COMPLEX" >> "$REPORT"
  if [ "$COMPLEX" -gt 0 ]; then
    gocyclo -over 10 . >> "$REPORT" 2>&1
    echo "WARNING: complexity violations" >> "$REPORT"
  fi
fi

cat "$REPORT"
if [ "$PASS" = "false" ]; then
  printf 'phase1-gates-FAIL'
  exit 1
fi
printf 'phase1-gates-PASS'