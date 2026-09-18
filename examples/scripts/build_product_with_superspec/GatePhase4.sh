set -eu
REPORT=".ai/gates/phase4.txt"
PASS=true
echo "=== Phase 4 Quality Gates ===" > "$REPORT"

if [ -f go.mod ]; then
  go build ./... >> "$REPORT" 2>&1 || PASS=false
  go test ./... >> "$REPORT" 2>&1 || PASS=false
elif [ -f pyproject.toml ]; then
  uv run pytest >> "$REPORT" 2>&1 || PASS=false
fi

cat "$REPORT"
if [ "$PASS" = "false" ]; then
  printf 'phase4-gates-FAIL'
  exit 1
fi
printf 'phase4-gates-PASS'