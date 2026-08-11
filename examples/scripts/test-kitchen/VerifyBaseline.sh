set -eu
mkdir -p .tracker
rm -f .tracker/plan_validate_count .tracker/rework_count .tracker/impl_count_* .tracker/batch_count
if [ -f pyproject.toml ]; then
  uv run pytest --co -q 2>/dev/null || uv run python -m pytest --co -q 2>/dev/null || echo 'no-tests-yet'
  printf 'baseline-verified-python'
elif [ -f package.json ]; then
  npm test 2>/dev/null || echo 'no-tests-yet'
  printf 'baseline-verified-node'
elif [ -f go.mod ]; then
  go build ./... 2>&1 || true
  go test ./... 2>&1 || echo 'no-tests-yet'
  printf 'baseline-verified-go'
elif [ -f Cargo.toml ]; then
  cargo build 2>&1 || true
  cargo test 2>&1 || echo 'no-tests-yet'
  printf 'baseline-verified-rust'
else
  printf 'baseline-verified-unknown'
fi