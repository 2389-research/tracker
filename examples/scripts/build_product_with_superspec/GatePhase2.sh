set -eu
REPORT=".ai/gates/phase2.txt"
PASS=true
echo "=== Phase 2 Quality Gates ===" > "$REPORT"

# Build + test
if [ -f go.mod ]; then
  go build ./... >> "$REPORT" 2>&1 || PASS=false
  go test ./... -coverprofile=.ai/gates/phase2-coverage.out >> "$REPORT" 2>&1 || PASS=false
  go tool cover -func=.ai/gates/phase2-coverage.out | tail -1 >> "$REPORT"
elif [ -f pyproject.toml ]; then
  uv run pytest --cov --cov-report=term-missing >> "$REPORT" 2>&1 || PASS=false
fi

# QG-7: Data quality gates (if gold dataset exists)
if [ -d tests/gold ] || [ -d tests/golden ]; then
  echo "--- Gold Dataset Evaluation ---" >> "$REPORT"
  if [ -f go.mod ]; then
    go test ./... -run 'Gold|Eval' -v >> "$REPORT" 2>&1 || true
  elif [ -f pyproject.toml ]; then
    uv run pytest -k 'gold or eval' -v >> "$REPORT" 2>&1 || true
  fi
fi

cat "$REPORT"
if [ "$PASS" = "false" ]; then
  printf 'phase2-gates-FAIL'
  exit 1
fi
printf 'phase2-gates-PASS'