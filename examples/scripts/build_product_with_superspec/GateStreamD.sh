set -eu
REPORT=".ai/gates/stream-d.txt"
PASS=true
echo "=== Stream D Quality Gates ===" > "$REPORT"

if [ -f go.mod ]; then
  go build ./... >> "$REPORT" 2>&1 || PASS=false
  go test ./... >> "$REPORT" 2>&1 || PASS=false
elif [ -f pyproject.toml ]; then
  uv run pytest >> "$REPORT" 2>&1 || PASS=false
fi

cat "$REPORT"
if [ "$PASS" = "false" ]; then
  printf 'streamD-gates-FAIL'
  exit 1
fi
printf 'streamD-gates-PASS'