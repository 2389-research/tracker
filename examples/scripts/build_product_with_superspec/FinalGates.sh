set -eu
REPORT=".ai/gates/final.txt"
PASS=true
echo "=== FINAL QUALITY GATES ===" > "$REPORT"

# QG-2: Static quality
if [ -f go.mod ]; then
  echo "--- Build ---" >> "$REPORT"
  go build ./... >> "$REPORT" 2>&1 || PASS=false
  echo "--- Vet ---" >> "$REPORT"
  go vet ./... >> "$REPORT" 2>&1 || PASS=false
  echo "--- Tests + Coverage ---" >> "$REPORT"
  go test ./... -coverprofile=.ai/gates/final-coverage.out -count=1 >> "$REPORT" 2>&1 || PASS=false
  echo "--- Coverage Summary ---" >> "$REPORT"
  go tool cover -func=.ai/gates/final-coverage.out | tail -1 >> "$REPORT"
elif [ -f pyproject.toml ]; then
  echo "--- Lint ---" >> "$REPORT"
  uv run ruff check . >> "$REPORT" 2>&1 || PASS=false
  echo "--- Type Check ---" >> "$REPORT"
  uv run pyright . >> "$REPORT" 2>&1 || PASS=false
  echo "--- Tests + Coverage ---" >> "$REPORT"
  uv run pytest --cov --cov-report=term-missing >> "$REPORT" 2>&1 || PASS=false
fi

# QG-7: Gold dataset evaluation
if [ -f go.mod ]; then
  echo "--- Gold Dataset ---" >> "$REPORT"
  go test ./... -run 'Gold|Eval|Regression' -v >> "$REPORT" 2>&1 || true
elif [ -f pyproject.toml ]; then
  echo "--- Gold Dataset ---" >> "$REPORT"
  uv run pytest -k 'gold or eval or regression' -v >> "$REPORT" 2>&1 || true
fi

# QG-1: Traceability completeness
echo "--- Traceability ---" >> "$REPORT"
if [ -f docs/traceability.yaml ]; then
  PENDING=$(grep -c 'status: pending' docs/traceability.yaml || echo 0)
  NULL_IMPL=$(grep -c 'impl_ref: null' docs/traceability.yaml || echo 0)
  NULL_TEST=$(grep -c 'test_ref: null' docs/traceability.yaml || echo 0)
  echo "Pending requirements: $PENDING" >> "$REPORT"
  echo "Missing impl_ref: $NULL_IMPL" >> "$REPORT"
  echo "Missing test_ref: $NULL_TEST" >> "$REPORT"
  if [ "$PENDING" -gt 0 ] || [ "$NULL_IMPL" -gt 0 ]; then
    echo "TRACEABILITY INCOMPLETE" >> "$REPORT"
    PASS=false
  fi
else
  echo "docs/traceability.yaml NOT FOUND" >> "$REPORT"
  PASS=false
fi

# Clean worktree check
echo "--- Worktree Check ---" >> "$REPORT"
WORKTREES=$(git worktree list --porcelain | grep -c 'worktree' || echo 1)
if [ "$WORKTREES" -gt 1 ]; then
  echo "LEFTOVER WORKTREES" >> "$REPORT"
  git worktree list >> "$REPORT"
  PASS=false
fi
BUILD_BRANCHES=$(git branch | grep 'build/' | wc -l | tr -d ' ')
if [ "$BUILD_BRANCHES" -gt 0 ]; then
  echo "LEFTOVER BUILD BRANCHES" >> "$REPORT"
  git branch | grep 'build/' >> "$REPORT"
  PASS=false
fi

cat "$REPORT"
if [ "$PASS" = "false" ]; then
  printf 'final-gates-FAIL'
  exit 1
fi
printf 'final-gates-PASS'